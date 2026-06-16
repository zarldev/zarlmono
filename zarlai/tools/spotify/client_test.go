package spotify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/tools/spotify"
)

// mountDeviceMock registers /v1/me/player/devices returning a single
// active device, and /v1/me/player (transfer) returning 204. Tests
// that don't care about device-resolution mechanics call this once
// before mounting their op-specific handlers.
func mountDeviceMock(mux *http.ServeMux) {
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"devices":[{"id":"dev-1","name":"Test","type":"Computer","is_active":true,"is_restricted":false,"volume_percent":50}]}`)
	})
	mux.HandleFunc("/v1/me/player", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestTokenCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache")

	in := spotify.Token{
		AccessToken:  "at-123",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		RefreshToken: "rt-456",
		Scope:        "user-library-read",
		ExpiresAt:    1_700_000_000,
	}
	if err := spotify.WriteTokenCache(path, in); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := spotify.ReadTokenCache(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", out, in)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 0600", perm)
	}
}

func TestReadTokenCacheMissing(t *testing.T) {
	_, err := spotify.ReadTokenCache(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// fakeSpotify serves both the API ("/v1/...") and the accounts token
// endpoint ("/api/token") from a single test server so the Client can
// point at one base URL.
type fakeSpotify struct {
	callsPlay    atomic.Int32
	callsRefresh atomic.Int32
	refresh401   bool
	currentToken string
}

func newFakeSpotify(t *testing.T, initialToken string) (*httptest.Server, *fakeSpotify) {
	t.Helper()
	f := &fakeSpotify{currentToken: initialToken}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		f.callsPlay.Add(1)
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != f.currentToken {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"status":401,"message":"token expired"}}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		f.callsRefresh.Add(1)
		if f.refresh401 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		f.currentToken = "refreshed-token"
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "rt-new",
			"scope":         "user-library-read",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, f
}

func TestClientPassesThrough200(t *testing.T) {
	srv, fake := newFakeSpotify(t, "at-ok")
	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "at-ok", RefreshToken: "rt"})

	c, err := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret",
		CachePath: cachePath,
		APIBase:   srv.URL, AccountsBase: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL+"/v1/me/player/play", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if n := fake.callsPlay.Load(); n != 1 {
		t.Errorf("play calls = %d, want 1", n)
	}
	if n := fake.callsRefresh.Load(); n != 0 {
		t.Errorf("refresh calls = %d, want 0", n)
	}
}

func TestClientRefreshOn401(t *testing.T) {
	srv, fake := newFakeSpotify(t, "stale-token")
	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "different-token", RefreshToken: "rt"})

	c, err := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret",
		CachePath: cachePath,
		APIBase:   srv.URL, AccountsBase: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL+"/v1/me/player/play", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (after refresh)", resp.StatusCode)
	}
	if n := fake.callsPlay.Load(); n != 2 {
		t.Errorf("play calls = %d, want 2 (first 401, retry 204)", n)
	}
	if n := fake.callsRefresh.Load(); n != 1 {
		t.Errorf("refresh calls = %d, want 1", n)
	}

	// Refreshed token was persisted.
	tok, err := spotify.ReadTokenCache(cachePath)
	if err != nil {
		t.Fatalf("ReadTokenCache: %v", err)
	}
	if tok.AccessToken != "refreshed-token" {
		t.Errorf("persisted access token = %q, want refreshed-token", tok.AccessToken)
	}
}

func TestClientRefreshFailsBubbles(t *testing.T) {
	srv, fake := newFakeSpotify(t, "stale-token")
	fake.refresh401 = true
	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "different", RefreshToken: "rt"})

	c, err := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret",
		CachePath: cachePath,
		APIBase:   srv.URL, AccountsBase: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL+"/v1/me/player/play", nil)
	_, err = c.Do(req)
	if err == nil {
		t.Fatal("expected error when refresh fails, got nil")
	}
	if !strings.Contains(err.Error(), "refresh") {
		t.Errorf("error should mention refresh, got: %v", err)
	}
}

func TestClientSearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "wolfpack" {
			t.Errorf("query q = %q, want wolfpack", got)
		}
		if got := r.URL.Query().Get("type"); got != "track" {
			t.Errorf("query type = %q, want track", got)
		}
		if got := r.URL.Query().Get("limit"); got != "3" {
			t.Errorf("query limit = %q, want 3", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"tracks": map[string]any{
				"items": []map[string]any{
					{
						"uri":     "spotify:track:abc",
						"name":    "Man Made of Meat",
						"artists": []map[string]any{{"name": "Viagra Boys"}},
						"album":   map[string]any{"name": "Welfare Jazz"},
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	hits, err := c.Search(t.Context(), "wolfpack", "track", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if hits[0].URI != "spotify:track:abc" {
		t.Errorf("uri = %q", hits[0].URI)
	}
	if hits[0].Name != "Man Made of Meat" {
		t.Errorf("name = %q", hits[0].Name)
	}
	if hits[0].Artist != "Viagra Boys" {
		t.Errorf("artist = %q", hits[0].Artist)
	}
}

func TestClientPlayTrack(t *testing.T) {
	var gotBody map[string]any
	var playQuery url.Values
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		playQuery = r.URL.Query()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	if err := c.Play(t.Context(), "spotify:track:abc", "track"); err != nil {
		t.Fatalf("Play track: %v", err)
	}
	uris, _ := gotBody["uris"].([]any)
	if len(uris) != 1 || uris[0] != "spotify:track:abc" {
		t.Errorf("uris = %v, want [spotify:track:abc]", uris)
	}
	if _, has := gotBody["context_uri"]; has {
		t.Errorf("track play should NOT include context_uri; body = %v", gotBody)
	}
	if got := playQuery.Get("device_id"); got != "dev-1" {
		t.Errorf("play missing device_id query: got %q", got)
	}
}

func TestClientPlayContext(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	if err := c.Play(t.Context(), "spotify:album:xyz", "album"); err != nil {
		t.Fatalf("Play album: %v", err)
	}
	if got := gotBody["context_uri"]; got != "spotify:album:xyz" {
		t.Errorf("context_uri = %v, want spotify:album:xyz", got)
	}
	if _, has := gotBody["uris"]; has {
		t.Errorf("album play should NOT include uris; body = %v", gotBody)
	}
}

func TestClientPauseAndSkip(t *testing.T) {
	var pauseHits, nextHits atomic.Int32
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/pause", func(w http.ResponseWriter, r *http.Request) {
		pauseHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/me/player/next", func(w http.ResponseWriter, r *http.Request) {
		nextHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	if err := c.Pause(t.Context()); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := c.Skip(t.Context(), 3); err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if n := pauseHits.Load(); n != 1 {
		t.Errorf("pause hits = %d, want 1", n)
	}
	if n := nextHits.Load(); n != 3 {
		t.Errorf("next hits = %d, want 3 (skip count)", n)
	}
}

func TestClientPlayInContext(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	if err := c.PlayInContext(t.Context(), "spotify:album:xyz", "spotify:track:abc"); err != nil {
		t.Fatalf("PlayInContext: %v", err)
	}
	if got := gotBody["context_uri"]; got != "spotify:album:xyz" {
		t.Errorf("context_uri = %v, want spotify:album:xyz", got)
	}
	offset, _ := gotBody["offset"].(map[string]any)
	if offset["uri"] != "spotify:track:abc" {
		t.Errorf("offset.uri = %v, want spotify:track:abc", offset["uri"])
	}
}

func TestClientGetTrackAlbumURI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tracks/abc", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"album": map[string]any{"uri": "spotify:album:zzz"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	got, err := c.GetTrackAlbumURI(t.Context(), "spotify:track:abc")
	if err != nil {
		t.Fatalf("GetTrackAlbumURI: %v", err)
	}
	if got != "spotify:album:zzz" {
		t.Errorf("got %q, want spotify:album:zzz", got)
	}
}

func TestClientQueueAdd(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/queue", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	if err := c.QueueAdd(t.Context(), "spotify:track:abc"); err != nil {
		t.Fatalf("QueueAdd: %v", err)
	}
	if !strings.Contains(gotQuery, "uri=spotify%3Atrack%3Aabc") {
		t.Errorf("query = %q, should contain uri=spotify:track:abc (url-encoded)", gotQuery)
	}
}

func TestClientNowPlaying(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/currently-playing", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"is_playing": true,
			"item": map[string]any{
				"name":    "Sports",
				"artists": []map[string]any{{"name": "Viagra Boys"}},
				"album":   map[string]any{"name": "Street Worms"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	np, err := c.NowPlaying(t.Context())
	if err != nil {
		t.Fatalf("NowPlaying: %v", err)
	}
	if !np.IsPlaying {
		t.Error("expected IsPlaying true")
	}
	if np.Track != "Sports" {
		t.Errorf("Track = %q", np.Track)
	}
	if np.Artist != "Viagra Boys" {
		t.Errorf("Artist = %q", np.Artist)
	}
	if np.Album != "Street Worms" {
		t.Errorf("Album = %q", np.Album)
	}
}

// TestClientPlayNoDevice covers the critical path for the "dead state"
// fix: when Spotify reports zero devices, Play returns a clear error
// rather than silently 204-ing into the void.
func TestClientPlayNoDevice(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"devices":[]}`)
	})
	playHit := false
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		playHit = true
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	err := c.Play(t.Context(), "spotify:track:abc", "track")
	if err == nil {
		t.Fatal("expected error when no device available, got nil")
	}
	if !strings.Contains(err.Error(), "no spotify device") {
		t.Errorf("error = %q, want it to mention no device", err.Error())
	}
	if playHit {
		t.Error("/play must not be called when no device is available")
	}
}

// TestClientPlayHonoursPreferredDevice covers the Spotify B path:
// when a preference is pinned and visible, it overrides Spotify's
// active pick, transferring playback when needed.
func TestClientPlayHonoursPreferredDevice(t *testing.T) {
	var transferBody, playQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"devices":[
			{"id":"phone-id","name":"Phone","type":"Smartphone","is_active":true,"is_restricted":false},
			{"id":"office-id","name":"Office Speaker","type":"Speaker","is_active":false,"is_restricted":false}
		]}`)
	})
	mux.HandleFunc("/v1/me/player", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		transferBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		playQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	pref := &stubPref{name: "office speaker"} // case-insensitive
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
		Preference: pref,
	})

	if err := c.Play(t.Context(), "spotify:track:abc", "track"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if !strings.Contains(transferBody, "office-id") {
		t.Errorf("transfer body = %q, want it to mention office-id (preference should beat the active phone)", transferBody)
	}
	q, _ := url.ParseQuery(playQuery)
	if q.Get("device_id") != "office-id" {
		t.Errorf("play device_id = %q, want office-id", q.Get("device_id"))
	}
}

// TestClientPlayPreferredAlreadyActive — when the preferred device is
// already the active one, no transfer call is made.
func TestClientPlayPreferredAlreadyActive(t *testing.T) {
	transferHit := false
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"devices":[
			{"id":"office-id","name":"Office Speaker","type":"Speaker","is_active":true,"is_restricted":false}
		]}`)
	})
	mux.HandleFunc("/v1/me/player", func(w http.ResponseWriter, r *http.Request) {
		transferHit = true
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	pref := &stubPref{name: "Office Speaker"}
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
		Preference: pref,
	})

	if err := c.Play(t.Context(), "", ""); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if transferHit {
		t.Error("transfer should not be called when the preferred device is already active")
	}
}

// TestClientPlayPreferredMissingFallsBack — pinned name not in current
// device list → fall through to active / first-available.
func TestClientPlayPreferredMissingFallsBack(t *testing.T) {
	var playQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"devices":[
			{"id":"phone-id","name":"Phone","type":"Smartphone","is_active":true,"is_restricted":false}
		]}`)
	})
	mux.HandleFunc("/v1/me/player", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		playQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	pref := &stubPref{name: "Office Speaker"} // not in the list
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
		Preference: pref,
	})

	if err := c.Play(t.Context(), "", ""); err != nil {
		t.Fatalf("Play: %v", err)
	}
	q, _ := url.ParseQuery(playQuery)
	if q.Get("device_id") != "phone-id" {
		t.Errorf("play device_id = %q, want phone-id (fallback to active)", q.Get("device_id"))
	}
}

// stubPref is a one-line DevicePreference for client tests; the
// fuller fakePreference lives in tools_test.go.
type stubPref struct{ name string }

func (p *stubPref) Preferred(_ context.Context) (string, error)    { return p.name, nil }
func (p *stubPref) SetPreferred(_ context.Context, n string) error { p.name = n; return nil }

// TestClientPlayTransfersWhenInactive verifies that when the only
// device is non-active, Play transfers playback to it before issuing
// /play. Without this, the play call lands on Spotify's stale
// last-known-active device — the root cause of the "dead state" bug.
func TestClientPlayTransfersWhenInactive(t *testing.T) {
	var transferBody, playQuery string
	transferHit, playHit := false, false
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"devices":[{"id":"sleeping-speaker","name":"Speaker","type":"Speaker","is_active":false,"is_restricted":false}]}`)
	})
	mux.HandleFunc("/v1/me/player", func(w http.ResponseWriter, r *http.Request) {
		transferHit = true
		b, _ := io.ReadAll(r.Body)
		transferBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		playHit = true
		playQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, _ := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})

	if err := c.Play(t.Context(), "spotify:track:abc", "track"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if !transferHit {
		t.Error("transfer playback was not called for an inactive device")
	}
	if !strings.Contains(transferBody, "sleeping-speaker") {
		t.Errorf("transfer body = %q, want it to mention sleeping-speaker", transferBody)
	}
	if !playHit {
		t.Error("/play was not called after transfer")
	}
	q, _ := url.ParseQuery(playQuery)
	if q.Get("device_id") != "sleeping-speaker" {
		t.Errorf("play device_id = %q, want sleeping-speaker", q.Get("device_id"))
	}
}
