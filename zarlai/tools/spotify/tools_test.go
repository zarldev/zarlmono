package spotify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/spotify"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// fakePreference is an in-memory DevicePreference for tests. Holds the
// pinned name behind a mutex so concurrent table-driven tests don't
// race.
type fakePreference struct {
	mu   sync.Mutex
	name string
}

func (p *fakePreference) Preferred(_ context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name, nil
}

func (p *fakePreference) SetPreferred(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.name = name
	return nil
}

func newTestClientWithPref(t *testing.T, handler http.Handler, pref spotify.DevicePreference) *spotify.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, err := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
		Preference: pref,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func newTestClient(t *testing.T, handler http.Handler) *spotify.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cachePath := filepath.Join(t.TempDir(), "cache")
	spotify.WriteTokenCache(cachePath, spotify.Token{AccessToken: "ok", RefreshToken: "rt"})
	c, err := spotify.NewClient(spotify.Config{
		ClientID: "id", ClientSecret: "secret", CachePath: cachePath,
		APIBase: srv.URL, AccountsBase: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestSearchTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tracks": map[string]any{"items": []map[string]any{
				{"uri": "spotify:track:abc", "name": "Sports",
					"artists": []map[string]any{{"name": "Viagra Boys"}},
					"album":   map[string]any{"name": "Street Worms"}},
			}},
		})
	})
	tool := spotify.NewSearchTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"query": "viagra boys"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	content := service.ToolResultText(res)
	for _, want := range []string{"Sports", "Viagra Boys", "spotify:track:abc"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q; got:\n%s", want, content)
		}
	}
}

func TestPlayToolPlaysTrackWithAlbumContext(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/tracks/abc", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"album": map[string]any{"uri": "spotify:album:zzz"},
		})
	})
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	tool := spotify.NewPlayTool(newTestClient(t, mux))

	// Quote-wrapped ID — simulates qwen's quirk. Track plays inside its
	// album so playback continues after the song ends.
	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"id": `"spotify:track:abc"`,
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	if got := gotBody["context_uri"]; got != "spotify:album:zzz" {
		t.Errorf("context_uri = %v, want spotify:album:zzz", got)
	}
	offset, _ := gotBody["offset"].(map[string]any)
	if offset["uri"] != "spotify:track:abc" {
		t.Errorf("offset.uri = %v, want spotify:track:abc", offset["uri"])
	}
	if _, has := gotBody["uris"]; has {
		t.Errorf("track-in-context play should NOT include uris; body = %v", gotBody)
	}
}

func TestPlayToolFallsBackOnAlbumLookupFailure(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/tracks/abc", func(w http.ResponseWriter, r *http.Request) {
		// Spotify can't find the album (region block, local file, etc).
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	tool := spotify.NewPlayTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"id": "spotify:track:abc",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	uris, _ := gotBody["uris"].([]any)
	if len(uris) != 1 || uris[0] != "spotify:track:abc" {
		t.Errorf("fallback should send uris=[track]; body = %v", gotBody)
	}
}

func TestPlayToolAlbumURIBypassesLookup(t *testing.T) {
	var gotBody map[string]any
	var trackHits int
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/tracks/", func(w http.ResponseWriter, r *http.Request) {
		trackHits++
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	tool := spotify.NewPlayTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"id": "spotify:album:xyz",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	if trackHits != 0 {
		t.Errorf("album play should not hit /v1/tracks/, got %d calls", trackHits)
	}
	if got := gotBody["context_uri"]; got != "spotify:album:xyz" {
		t.Errorf("context_uri = %v, want spotify:album:xyz", got)
	}
}

func TestPlayToolResume(t *testing.T) {
	var gotBody []byte
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/play", func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	})
	tool := spotify.NewPlayTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	if len(gotBody) != 0 {
		t.Errorf("resume should send empty body, got: %q", gotBody)
	}
}

func TestPauseTool(t *testing.T) {
	hit := false
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/pause", func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusNoContent)
	})
	tool := spotify.NewPauseTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	if !hit {
		t.Error("pause endpoint not called")
	}
}

func TestSkipTool(t *testing.T) {
	hits := 0
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/next", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNoContent)
	})
	tool := spotify.NewSkipTool(newTestClient(t, mux))

	// Default (no count) → 1 skip.
	if _, err := tool.Execute(t.Context(), tools.ToolCall{}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if hits != 1 {
		t.Errorf("after default skip, hits = %d, want 1", hits)
	}

	// Explicit count.
	if _, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"count": float64(3)}}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if hits != 4 {
		t.Errorf("after count=3 skip, hits = %d, want 4", hits)
	}
}

func TestQueueAddTool(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mountDeviceMock(mux)
	mux.HandleFunc("/v1/me/player/queue", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})
	tool := spotify.NewQueueAddTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"id": "spotify:track:abc"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	if !strings.Contains(gotQuery, "uri=spotify%3Atrack%3Aabc") {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestQueueAddToolRejectsAlbum(t *testing.T) {
	mux := http.NewServeMux() // no handlers — should never be hit
	tool := spotify.NewQueueAddTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"id": "spotify:album:xyz"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Success {
		t.Fatalf("expected failure, got %q", service.ToolResultText(res))
	}
}

func TestListDevicesTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"devices":[
			{"id":"a","name":"Kitchen","type":"Speaker","is_active":true,"is_restricted":false,"volume_percent":50},
			{"id":"b","name":"Phone","type":"Smartphone","is_active":false,"is_restricted":false,"volume_percent":80}
		]}`)
	})
	tool := spotify.NewListDevicesTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	content := service.ToolResultText(res)
	for _, want := range []string{"Kitchen", "Speaker", "active", "Phone", "Smartphone"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q; got:\n%s", want, content)
		}
	}
}

func TestListDevicesToolEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"devices":[]}`)
	})
	tool := spotify.NewListDevicesTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	content := service.ToolResultText(res)
	if !strings.Contains(content, "No Spotify devices visible") {
		t.Errorf("expected empty-state message, got: %s", content)
	}
}

func TestSetPreferredDeviceToolPinsKnownDevice(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"devices":[
			{"id":"a","name":"Kitchen","type":"Speaker","is_active":true,"is_restricted":false}
		]}`)
	})
	pref := &fakePreference{}
	tool := spotify.NewSetPreferredDeviceTool(newTestClientWithPref(t, mux, pref))

	// Case-insensitive match resolves to the canonical device name.
	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"name": "kitchen"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	if !strings.Contains(service.ToolResultText(res), "Kitchen") {
		t.Errorf("response should echo canonical name; got: %s", service.ToolResultText(res))
	}
	saved, _ := pref.Preferred(t.Context())
	if saved != "Kitchen" {
		t.Errorf("saved name = %q, want %q", saved, "Kitchen")
	}
}

func TestSetPreferredDeviceToolRejectsUnknown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/devices", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"devices":[
			{"id":"a","name":"Kitchen","type":"Speaker","is_active":true,"is_restricted":false}
		]}`)
	})
	pref := &fakePreference{name: "Phone"}
	tool := spotify.NewSetPreferredDeviceTool(newTestClientWithPref(t, mux, pref))

	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"name": "Speaker"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Success {
		t.Fatalf("expected failure, got %q", service.ToolResultText(res))
	}
	if !strings.Contains(res.Error, "Kitchen") {
		t.Errorf("error should list available devices; got: %v", res.Error)
	}
	// The pinned value must not be overwritten on rejection.
	saved, _ := pref.Preferred(t.Context())
	if saved != "Phone" {
		t.Errorf("saved name = %q, must remain %q on error", saved, "Phone")
	}
}

func TestSetPreferredDeviceToolClears(t *testing.T) {
	pref := &fakePreference{name: "Kitchen"}
	// Empty name short-circuits — no /devices call needed.
	tool := spotify.NewSetPreferredDeviceTool(newTestClientWithPref(t, http.NewServeMux(), pref))

	res, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"name": ""}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	saved, _ := pref.Preferred(t.Context())
	if saved != "" {
		t.Errorf("saved name = %q, want empty", saved)
	}
}

func TestNowPlayingTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/me/player/currently-playing", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"is_playing": true,
			"item": map[string]any{"name": "Sports",
				"artists": []map[string]any{{"name": "Viagra Boys"}},
				"album":   map[string]any{"name": "Street Worms"}},
			"device": map[string]any{"name": "Kitchen"},
		})
	})
	tool := spotify.NewNowPlayingTool(newTestClient(t, mux))

	res, err := tool.Execute(t.Context(), tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unexpected tool failure: %s", res.Error)
	}
	content := service.ToolResultText(res)
	for _, want := range []string{"Sports", "Viagra Boys", "Street Worms", "Kitchen"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q; got: %s", want, content)
		}
	}
}
