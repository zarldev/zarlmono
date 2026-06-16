package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Token matches spotipy's CacheFileHandler JSON layout, so tokens
// minted by cmd/spotify-auth round-trip through this client without
// translation.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	ExpiresAt    int64  `json:"expires_at"`
}

// ReadTokenCache loads a Token from the given path.
func ReadTokenCache(path string) (Token, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Token{}, fmt.Errorf("read token cache: %w", err)
	}
	var t Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return Token{}, fmt.Errorf("parse token cache: %w", err)
	}
	return t, nil
}

// WriteTokenCache writes a Token to disk as JSON with 0600 perms,
// creating parent dirs (0700) if needed.
func WriteTokenCache(path string, tok Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir token cache: %w", err)
	}
	raw, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write token cache: %w", err)
	}
	return nil
}

// Spotify Web API hosts. Overrideable via Config for tests.
const (
	DefaultAPIBase      = "https://api.spotify.com"
	DefaultAccountsBase = "https://accounts.spotify.com"
)

// DevicePreference reads and writes a user's pinned Spotify Connect
// device by name. When set on Config, the Client consults Preferred
// first during device resolution — if a device with that name is
// currently visible, it wins over Spotify's "last active" pick. Names
// (not IDs) so users can write the value as it appears in the Spotify
// app, and so a re-paired speaker keeps working.
type DevicePreference interface {
	Preferred(ctx context.Context) (string, error)
	SetPreferred(ctx context.Context, name string) error
}

// Config configures a Spotify Client. All fields except APIBase,
// AccountsBase, and Preference are required — production callers get
// defaults for the two bases; tests override them to point at an
// httptest.Server. Preference is optional; nil means no pinning.
type Config struct {
	ClientID     string
	ClientSecret string
	CachePath    string
	APIBase      string
	AccountsBase string
	Preference   DevicePreference
}

// Client is the shared Spotify Web API HTTP client. It adds the bearer
// token to every request and transparently refreshes on 401.
type Client struct {
	cfg  Config
	http *http.Client

	mu    sync.Mutex
	token Token
}

// NewClient validates config, loads the on-disk token cache, and
// returns a ready Client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.CachePath == "" {
		return nil, fmt.Errorf("spotify: client_id, client_secret, cache_path required")
	}
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	if cfg.AccountsBase == "" {
		cfg.AccountsBase = DefaultAccountsBase
	}
	tok, err := ReadTokenCache(cfg.CachePath)
	if err != nil {
		return nil, fmt.Errorf("spotify: %w (run cmd/spotify-auth)", err)
	}
	return &Client{
		cfg:   cfg,
		http:  &http.Client{Timeout: 15 * time.Second},
		token: tok,
	}, nil
}

// Do sends a request with the current bearer token. On 401 it
// refreshes the token, persists it, and retries once. Any non-401
// non-2xx response is returned as-is for the caller to decode.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	req.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	c.mu.Unlock()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spotify http: %w", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	resp.Body.Close()

	if err := c.refresh(req.Context()); err != nil {
		return nil, fmt.Errorf("spotify refresh: %w", err)
	}

	// Retry with the refreshed token. Must rebuild the request body
	// reader since Do drained it.
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		body, berr := req.GetBody()
		if berr != nil {
			return nil, fmt.Errorf("spotify rebuild body: %w", berr)
		}
		retry.Body = body
	}
	c.mu.Lock()
	retry.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	c.mu.Unlock()
	return c.http.Do(retry)
}

func (c *Client) refresh(ctx context.Context) error {
	c.mu.Lock()
	rt := c.token.RefreshToken
	c.mu.Unlock()
	if rt == "" {
		return fmt.Errorf("no refresh token in cache")
	}
	body := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.AccountsBase+"/api/token", strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("build refresh request: %w", err)
	}
	req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("refresh http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh status %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	var tok Token
	if err := json.Unmarshal(raw, &tok); err != nil {
		return fmt.Errorf("decode refresh response: %w", err)
	}
	tok.ExpiresAt = time.Now().Unix() + int64(tok.ExpiresIn)

	c.mu.Lock()
	// Spotify often omits refresh_token on refresh responses; keep the
	// one we already have in that case.
	if tok.RefreshToken == "" {
		tok.RefreshToken = c.token.RefreshToken
	}
	c.token = tok
	c.mu.Unlock()

	if err := WriteTokenCache(c.cfg.CachePath, tok); err != nil {
		return fmt.Errorf("persist refreshed token: %w", err)
	}
	return nil
}

// SearchHit is one result row from the search API, flattened from the
// raw Spotify response.
type SearchHit struct {
	URI    string
	Name   string
	Artist string
	Album  string
}

// NowPlayingInfo is the flattened currently-playing response.
type NowPlayingInfo struct {
	IsPlaying bool
	Track     string
	Artist    string
	Album     string
	Device    string
	// ImageURL is the album-art URL Spotify returns for the current
	// track, picked from the `images` array at the size closest to what
	// the now-playing strip wants (~64px). Empty when no artwork is
	// available (podcasts, local files, episode content).
	ImageURL string
}

// Search queries Spotify's /v1/search for the given type (track, album,
// artist, or playlist) and returns up to limit hits.
func (c *Client) Search(ctx context.Context, query, kind string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 10
	}
	q := url.Values{
		"q":     {query},
		"type":  {kind},
		"limit": {fmt.Sprintf("%d", limit)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.cfg.APIBase+"/v1/search?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build search request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeAPIError(resp, "search")
	}

	var body struct {
		Tracks    searchBucket `json:"tracks"`
		Albums    searchBucket `json:"albums"`
		Artists   searchBucket `json:"artists"`
		Playlists searchBucket `json:"playlists"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	var bucket searchBucket
	switch kind {
	case "track":
		bucket = body.Tracks
	case "album":
		bucket = body.Albums
	case "artist":
		bucket = body.Artists
	case "playlist":
		bucket = body.Playlists
	default:
		return nil, fmt.Errorf("search: unsupported kind %q", kind)
	}
	hits := make([]SearchHit, 0, len(bucket.Items))
	for _, it := range bucket.Items {
		h := SearchHit{URI: it.URI, Name: it.Name, Album: it.Album.Name}
		if len(it.Artists) > 0 {
			h.Artist = it.Artists[0].Name
		}
		hits = append(hits, h)
	}
	return hits, nil
}

type searchBucket struct {
	Items []searchItem `json:"items"`
}

type searchItem struct {
	URI     string `json:"uri"`
	Name    string `json:"name"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name string `json:"name"`
	} `json:"album"`
}

// Device is a Spotify Connect device the account has access to.
type Device struct {
	ID           string
	Name         string
	Type         string
	IsActive     bool
	IsRestricted bool
	Volume       int
}

// ListDevices returns the account's Spotify Connect devices.
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.cfg.APIBase+"/v1/me/player/devices", nil)
	if err != nil {
		return nil, fmt.Errorf("build devices request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeAPIError(resp, "list devices")
	}
	var body struct {
		Devices []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Type          string `json:"type"`
			IsActive      bool   `json:"is_active"`
			IsRestricted  bool   `json:"is_restricted"`
			VolumePercent int    `json:"volume_percent"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode devices: %w", err)
	}
	devs := make([]Device, 0, len(body.Devices))
	for _, d := range body.Devices {
		devs = append(devs, Device{
			ID: d.ID, Name: d.Name, Type: d.Type,
			IsActive: d.IsActive, IsRestricted: d.IsRestricted,
			Volume: d.VolumePercent,
		})
	}
	return devs, nil
}

// resolveDevice picks a device for a player call. If a preferred
// device name is configured and currently visible, it wins outright;
// otherwise prefers a currently active device, falling back to the
// first non-restricted device. Returns (deviceID, alreadyActive,
// error). Error message is user-facing — surfaces directly to the
// assistant when no device is reachable.
func (c *Client) resolveDevice(ctx context.Context) (string, bool, error) {
	devs, err := c.ListDevices(ctx)
	if err != nil {
		return "", false, fmt.Errorf("resolve device: %w", err)
	}
	if c.cfg.Preference != nil {
		pref, err := c.cfg.Preference.Preferred(ctx)
		if err != nil {
			return "", false, fmt.Errorf("read preferred device: %w", err)
		}
		if pref != "" {
			for _, d := range devs {
				if d.ID != "" && strings.EqualFold(d.Name, pref) {
					return d.ID, d.IsActive, nil
				}
			}
		}
	}
	for _, d := range devs {
		if d.IsActive && d.ID != "" {
			return d.ID, true, nil
		}
	}
	for _, d := range devs {
		if !d.IsRestricted && d.ID != "" {
			return d.ID, false, nil
		}
	}
	return "", false, fmt.Errorf("no spotify device available — open spotify on a phone, desktop, or speaker first")
}

// transferPlayback hands control to deviceID. Idempotent: Spotify
// no-ops when the device is already active.
func (c *Client) transferPlayback(ctx context.Context, deviceID string) error {
	body, err := json.Marshal(map[string]any{
		"device_ids": []string{deviceID},
	})
	if err != nil {
		return fmt.Errorf("marshal transfer body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.cfg.APIBase+"/v1/me/player", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build transfer request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	bodyCopy := body
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyCopy)), nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("transfer playback: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp, "transfer playback")
	}
	return nil
}

// Play starts playback. For kind="track" the URI goes into the uris
// array; for album/artist/playlist it goes into context_uri. An empty
// uri resumes current playback.
//
// Always resolves a concrete device first and pins the call to it via
// device_id, transferring playback if Spotify says no device is
// currently active. Without this, a play call with no body lands on
// Spotify's last-known active device, which silently goes stale after
// the device app idles — leaving the API returning 204 while no audio
// actually plays.
func (c *Client) Play(ctx context.Context, uri, kind string) error {
	deviceID, active, err := c.resolveDevice(ctx)
	if err != nil {
		return err
	}
	if !active {
		if err := c.transferPlayback(ctx, deviceID); err != nil {
			return err
		}
	}
	var body []byte
	if uri != "" {
		var payload map[string]any
		if kind == "track" {
			payload = map[string]any{"uris": []string{uri}}
		} else {
			payload = map[string]any{"context_uri": uri}
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal play body: %w", err)
		}
		body = b
	}
	q := url.Values{"device_id": {deviceID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.cfg.APIBase+"/v1/me/player/play?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build play request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		bodyCopy := body
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyCopy)), nil
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp, "play")
	}
	return nil
}

// PlayInContext starts playback of a track inside a parent context
// (album, playlist, or artist), so playback continues with the rest of
// the context after the track ends. Calling Play with a bare track URI
// only loads that one track and then stops; this preserves the
// "everyday" behaviour of clicking a song in the Spotify UI, which
// implicitly plays through the surrounding album.
func (c *Client) PlayInContext(ctx context.Context, contextURI, offsetURI string) error {
	if contextURI == "" || offsetURI == "" {
		return fmt.Errorf("play in context: context and offset URIs are required")
	}
	deviceID, active, err := c.resolveDevice(ctx)
	if err != nil {
		return err
	}
	if !active {
		if err := c.transferPlayback(ctx, deviceID); err != nil {
			return err
		}
	}
	body, err := json.Marshal(map[string]any{
		"context_uri": contextURI,
		"offset":      map[string]string{"uri": offsetURI},
	})
	if err != nil {
		return fmt.Errorf("marshal play body: %w", err)
	}
	q := url.Values{"device_id": {deviceID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.cfg.APIBase+"/v1/me/player/play?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build play request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	bodyCopy := body
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyCopy)), nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp, "play in context")
	}
	return nil
}

// GetTrackAlbumURI looks up a track and returns its album's spotify URI.
// Used by PlayTool to find the natural context for a bare-track play
// request — without it, single-track playback ends after one song.
func (c *Client) GetTrackAlbumURI(ctx context.Context, trackURI string) (string, error) {
	id := strings.TrimPrefix(trackURI, "spotify:track:")
	if id == trackURI || id == "" {
		return "", fmt.Errorf("get track album: %q is not a track URI", trackURI)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.cfg.APIBase+"/v1/tracks/"+url.PathEscape(id), nil)
	if err != nil {
		return "", fmt.Errorf("build track request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", decodeAPIError(resp, "get track")
	}
	var body struct {
		Album struct {
			URI string `json:"uri"`
		} `json:"album"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode track: %w", err)
	}
	if body.Album.URI == "" {
		return "", fmt.Errorf("get track %s: response had no album.uri", trackURI)
	}
	return body.Album.URI, nil
}

// Pause pauses the active device. Resolves the device first and pins
// the call to it; if there's no device at all, returns the resolver's
// "open spotify first" error rather than Spotify's vague 404.
func (c *Client) Pause(ctx context.Context) error {
	deviceID, _, err := c.resolveDevice(ctx)
	if err != nil {
		return err
	}
	q := url.Values{"device_id": {deviceID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.cfg.APIBase+"/v1/me/player/pause?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("build pause request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp, "pause")
	}
	return nil
}

// Skip advances the current queue by count tracks (each "next" is one
// POST to Spotify — the API has no bulk-skip).
func (c *Client) Skip(ctx context.Context, count int) error {
	if count <= 0 {
		count = 1
	}
	deviceID, _, err := c.resolveDevice(ctx)
	if err != nil {
		return err
	}
	q := url.Values{"device_id": {deviceID}}
	target := c.cfg.APIBase + "/v1/me/player/next?" + q.Encode()
	for i := 0; i < count; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
		if err != nil {
			return fmt.Errorf("build skip request: %w", err)
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			return decodeAPIError(resp, "skip")
		}
	}
	return nil
}

// QueueAdd appends a track URI to the active device's queue.
func (c *Client) QueueAdd(ctx context.Context, trackURI string) error {
	deviceID, _, err := c.resolveDevice(ctx)
	if err != nil {
		return err
	}
	q := url.Values{"uri": {trackURI}, "device_id": {deviceID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.APIBase+"/v1/me/player/queue?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("build queue request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp, "queue add")
	}
	return nil
}

// NowPlaying returns the currently playing item, or an empty
// NowPlayingInfo with IsPlaying=false when the device is idle.
func (c *Client) NowPlaying(ctx context.Context) (NowPlayingInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.cfg.APIBase+"/v1/me/player/currently-playing", nil)
	if err != nil {
		return NowPlayingInfo{}, fmt.Errorf("build currently-playing: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return NowPlayingInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return NowPlayingInfo{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return NowPlayingInfo{}, decodeAPIError(resp, "currently-playing")
	}
	var body struct {
		IsPlaying bool `json:"is_playing"`
		Item      struct {
			Name    string `json:"name"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Album struct {
				Name   string `json:"name"`
				Images []struct {
					URL    string `json:"url"`
					Width  int    `json:"width"`
					Height int    `json:"height"`
				} `json:"images"`
			} `json:"album"`
		} `json:"item"`
		Device struct {
			Name string `json:"name"`
		} `json:"device"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return NowPlayingInfo{}, fmt.Errorf("decode currently-playing: %w", err)
	}
	info := NowPlayingInfo{
		IsPlaying: body.IsPlaying,
		Track:     body.Item.Name,
		Album:     body.Item.Album.Name,
		Device:    body.Device.Name,
		ImageURL:  pickAlbumArt(body.Item.Album.Images),
	}
	if len(body.Item.Artists) > 0 {
		info.Artist = body.Item.Artists[0].Name
	}
	return info, nil
}

// pickAlbumArt returns the URL of the image closest to 64px on its
// longest edge — the now-playing strip is small, there's no reason to
// ship the 640px asset. Spotify returns images sorted largest-first, so
// the last entry is usually the right one; fall back to whatever exists
// when the array is short or the sizes are missing.
func pickAlbumArt(images []struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}) string {
	if len(images) == 0 {
		return ""
	}
	best := images[0].URL
	bestDelta := -1
	for _, img := range images {
		if img.URL == "" {
			continue
		}
		size := max(img.Height, img.Width)
		if size == 0 {
			continue
		}
		delta := size - 64
		if delta < 0 {
			delta = -delta
		}
		if bestDelta == -1 || delta < bestDelta {
			best = img.URL
			bestDelta = delta
		}
	}
	return best
}

// decodeAPIError reads the response body and wraps it in a Go error
// carrying the Spotify error message verbatim.
func decodeAPIError(resp *http.Response, op string) error {
	raw, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("spotify %s: status %d: %s", op, resp.StatusCode, bytes.TrimSpace(raw))
}
