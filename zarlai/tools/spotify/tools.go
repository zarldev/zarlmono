package spotify

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// SearchTool implements service.Tool for Spotify catalog search.
type SearchTool struct{ c *Client }

func NewSearchTool(c *Client) *SearchTool { return &SearchTool{c: c} }

func (t *SearchTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spotify_search",
		Description: "Search Spotify's catalog for tracks, albums, artists, or playlists. Returns each hit with its name, artist (for tracks), and spotify:TYPE:ID URI — pass that URI straight to spotify_play or spotify_queue_add.",
		Parameters: service.Parameters{
			{Name: "query", Type: service.ParamString, Description: "Search terms (artist, song, album, or a free-text phrase).", Required: true},
			{Name: "type", Type: service.ParamString, Description: "Kind of item to search for. One of: track, album, artist, playlist. Defaults to track.", Enum: []string{"track", "album", "artist", "playlist"}},
			{Name: "limit", Type: service.ParamInteger, Description: "Max hits (default 10, cap 20)."},
		}.ToJSONSchema(),
	}
}

func (t *SearchTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	query := strings.TrimSpace(call.Arguments.String("query", ""))
	if query == "" {
		return tools.Failure(call.ID, tools.Validation("spotify_search", "query is required")), nil
	}
	kind := call.Arguments.String("type", "")
	if kind == "" {
		kind = "track"
	}
	limit := call.Arguments.Int("limit", 0)
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}
	hits, err := t.c.Search(ctx, query, kind, limit)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("spotify_search", err)), nil
	}
	if len(hits) == 0 {
		return tools.Success(call.ID, "No matches on Spotify."), nil
	}
	var sb strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&sb, "%d. %s", i+1, h.Name)
		if h.Artist != "" {
			fmt.Fprintf(&sb, " — %s", h.Artist)
		}
		if h.Album != "" && kind == "track" {
			fmt.Fprintf(&sb, " (%s)", h.Album)
		}
		fmt.Fprintf(&sb, "\n   %s\n", h.URI)
	}
	return tools.Success(call.ID, strings.TrimRight(sb.String(), "\n")), nil
}

// PlayTool starts or resumes Spotify playback.
type PlayTool struct{ c *Client }

func NewPlayTool(c *Client) *PlayTool { return &PlayTool{c: c} }

func (t *PlayTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spotify_play",
		Description: "Start Spotify playback. Pass `id` as a bare Spotify ID (e.g. 5gR6gTGOGsg9zcR7JhvwQz), a spotify:TYPE:ID URI, or an open.spotify.com link — any form works. Omit `id` to resume what's currently queued on the active device. Single tracks are auto-played within their album so playback continues after the song ends instead of stopping in isolation; pass an album / artist / playlist URI directly to use that as the context. ALWAYS prefer this tool over Home Assistant call_service for Spotify content — going through HA's media_player.play_media with a Spotify URI plays the track in isolation with no continuation.",
		Parameters: service.Parameters{
			{Name: "id", Type: service.ParamString, Description: "Spotify ID, URI, or open.spotify.com link. Omit to resume current playback."},
		}.ToJSONSchema(),
	}
}

func (t *PlayTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	raw := call.Arguments.String("id", "")
	if strings.TrimSpace(raw) == "" {
		if err := t.c.Play(ctx, "", ""); err != nil {
			return tools.Failure(call.ID, tools.Transient("spotify_play", err)), nil
		}
		return tools.Success(call.ID, "Resumed playback."), nil
	}
	uri, kind, err := NormalizeID(raw)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("spotify_play", err.Error())), nil
	}
	if kind == "track" {
		albumURI, lookupErr := t.c.GetTrackAlbumURI(ctx, uri)
		if lookupErr == nil {
			if err := t.c.PlayInContext(ctx, albumURI, uri); err != nil {
				return tools.Failure(call.ID, tools.Transient("spotify_play", err)), nil
			}
			return tools.Success(call.ID, fmt.Sprintf("Playing %s within %s.", uri, albumURI)), nil
		}
		// Album lookup is best-effort — if Spotify can't tell us the
		// album (rare, but possible for local files / podcasts / region
		// blocks) fall back to the original single-track play so the
		// user still hears something. Continuation is lost in that case.
	}
	if err := t.c.Play(ctx, uri, kind); err != nil {
		return tools.Failure(call.ID, tools.Transient("spotify_play", err)), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("Playing %s.", uri)), nil
}

// PauseTool pauses Spotify playback.
type PauseTool struct{ c *Client }

func NewPauseTool(c *Client) *PauseTool { return &PauseTool{c: c} }

func (t *PauseTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spotify_pause",
		Description: "Pause the active Spotify device.",
		Parameters:  service.Parameters{}.ToJSONSchema(),
	}
}

func (t *PauseTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if err := t.c.Pause(ctx); err != nil {
		return tools.Failure(call.ID, tools.Transient("spotify_pause", err)), nil
	}
	return tools.Success(call.ID, "Paused."), nil
}

// SkipTool advances the current queue by N tracks.
type SkipTool struct{ c *Client }

func NewSkipTool(c *Client) *SkipTool { return &SkipTool{c: c} }

func (t *SkipTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spotify_skip",
		Description: "Skip to the next Spotify track. Pass `count` to skip multiple tracks in one go (default 1).",
		Parameters: service.Parameters{
			{Name: "count", Type: service.ParamInteger, Description: "Number of tracks to skip (default 1)."},
		}.ToJSONSchema(),
	}
}

func (t *SkipTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	count := call.Arguments.Int("count", 0)
	if err := t.c.Skip(ctx, count); err != nil {
		return tools.Failure(call.ID, tools.Transient("spotify_skip", err)), nil
	}
	if count <= 1 {
		return tools.Success(call.ID, "Skipped."), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("Skipped %d tracks.", count)), nil
}

// QueueAddTool appends a track URI to the Spotify queue.
type QueueAddTool struct{ c *Client }

func NewQueueAddTool(c *Client) *QueueAddTool { return &QueueAddTool{c: c} }

func (t *QueueAddTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spotify_queue_add",
		Description: "Append a track to the Spotify queue on the active device. `id` accepts a bare track ID, spotify:track:ID URI, or open.spotify.com link. Albums/artists/playlists are not supported — Spotify's queue API is track-only.",
		Parameters: service.Parameters{
			{Name: "id", Type: service.ParamString, Description: "Track ID, URI, or link.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *QueueAddTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	raw := call.Arguments.String("id", "")
	uri, kind, err := NormalizeID(raw)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("spotify_queue_add", err.Error())), nil
	}
	if kind != "track" {
		return tools.Failure(call.ID, tools.Validation("spotify_queue_add", fmt.Sprintf("spotify queue: only tracks can be queued, got %s", kind))), nil
	}
	if err := t.c.QueueAdd(ctx, uri); err != nil {
		return tools.Failure(call.ID, tools.Transient("spotify_queue_add", err)), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("Queued %s.", uri)), nil
}

// ListDevicesTool enumerates Spotify Connect devices visible to the
// account so the user (or the assistant) can pick one to pin.
type ListDevicesTool struct{ c *Client }

func NewListDevicesTool(c *Client) *ListDevicesTool { return &ListDevicesTool{c: c} }

func (t *ListDevicesTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spotify_list_devices",
		Description: "List Spotify Connect devices the account can see right now (phones, speakers, desktops). Each line shows the device name, type, and whether it's currently active. Use this to figure out which device name to pin as the preferred playback target.",
		Parameters:  service.Parameters{}.ToJSONSchema(),
	}
}

func (t *ListDevicesTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	devs, err := t.c.ListDevices(ctx)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("spotify_list_devices", err)), nil
	}
	if len(devs) == 0 {
		return tools.Success(call.ID, "No Spotify devices visible. Open Spotify on a phone, desktop, or speaker first."), nil
	}
	var sb strings.Builder
	for _, d := range devs {
		fmt.Fprintf(&sb, "- %s (%s)", d.Name, d.Type)
		if d.IsActive {
			sb.WriteString(" — active")
		}
		if d.IsRestricted {
			sb.WriteString(" — restricted")
		}
		sb.WriteString("\n")
	}
	return tools.Success(call.ID, strings.TrimRight(sb.String(), "\n")), nil
}

// SetPreferredDeviceTool pins a Spotify device by name so subsequent
// playback calls always target it. Validates that the named device is
// currently visible before saving — silently storing a typo would
// quietly break playback later.
type SetPreferredDeviceTool struct{ c *Client }

func NewSetPreferredDeviceTool(c *Client) *SetPreferredDeviceTool {
	return &SetPreferredDeviceTool{c: c}
}

func (t *SetPreferredDeviceTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spotify_set_preferred_device",
		Description: "Pin a Spotify Connect device by name as the preferred playback target. Future spotify_play / spotify_pause / spotify_skip calls will route there whenever it's visible. Pass an empty `name` to clear the pin and fall back to auto-pick. Use spotify_list_devices first to see exact names.",
		Parameters: service.Parameters{
			{Name: "name", Type: service.ParamString, Description: "Device name as shown in spotify_list_devices. Empty string clears the pin."},
		}.ToJSONSchema(),
	}
}

func (t *SetPreferredDeviceTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if t.c.cfg.Preference == nil {
		return tools.Failure(call.ID, tools.Transient("spotify_set_preferred_device", fmt.Errorf("preferred device storage not configured"))), nil
	}
	name := strings.TrimSpace(call.Arguments.String("name", ""))
	if name == "" {
		if err := t.c.cfg.Preference.SetPreferred(ctx, ""); err != nil {
			return tools.Failure(call.ID, tools.Transient("spotify_set_preferred_device", err)), nil
		}
		return tools.Success(call.ID, "Preferred Spotify device cleared. Auto-pick is back on."), nil
	}
	devs, err := t.c.ListDevices(ctx)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("spotify_set_preferred_device", err)), nil
	}
	for _, d := range devs {
		if strings.EqualFold(d.Name, name) {
			if err := t.c.cfg.Preference.SetPreferred(ctx, d.Name); err != nil {
				return tools.Failure(call.ID, tools.Transient("spotify_set_preferred_device", err)), nil
			}
			return tools.Success(call.ID, fmt.Sprintf("Preferred Spotify device set to %q.", d.Name)), nil
		}
	}
	var available []string
	for _, d := range devs {
		available = append(available, d.Name)
	}
	if len(available) == 0 {
		return tools.Failure(call.ID, tools.Validation("spotify_set_preferred_device", "no Spotify devices visible right now — open Spotify on the device first")), nil
	}
	return tools.Failure(call.ID, tools.Validation("spotify_set_preferred_device", fmt.Sprintf("no Spotify device named %q. Visible: %s", name, strings.Join(available, ", ")))), nil
}

// NowPlayingTool reports the current Spotify track + device.
type NowPlayingTool struct{ c *Client }

func NewNowPlayingTool(c *Client) *NowPlayingTool { return &NowPlayingTool{c: c} }

func (t *NowPlayingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spotify_now_playing",
		Description: "Report what's currently playing on Spotify, including the artist, album, and device name. Returns an idle notice when nothing is playing.",
		Parameters:  service.Parameters{}.ToJSONSchema(),
	}
}

func (t *NowPlayingTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	np, err := t.c.NowPlaying(ctx)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("spotify_now_playing", err)), nil
	}
	if np.Track == "" {
		return tools.Success(call.ID, "Nothing playing on Spotify."), nil
	}
	status := "playing"
	if !np.IsPlaying {
		status = "paused"
	}
	return tools.Success(call.ID, fmt.Sprintf(
		"%s: %s — %s (%s) on %s",
		status, np.Track, np.Artist, np.Album, np.Device,
	)), nil
}
