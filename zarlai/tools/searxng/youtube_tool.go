package searxng

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// YouTubeSearchTool searches for YouTube videos via SearXNG's videos category,
// filters to YouTube-hosted results, extracts the video IDs, and pushes a
// `videos` notification to the session's frontend for inline playback.
type YouTubeSearchTool struct {
	client        *Client
	notifications *znotify.NotificationStore
}

// NewYouTubeSearchTool creates a YouTube search tool. notifications may be nil
// for tests; production callers should pass the shared NotificationStore so the
// frontend floating player receives the results.
func NewYouTubeSearchTool(client *Client, notifications *znotify.NotificationStore) *YouTubeSearchTool {
	return &YouTubeSearchTool{client: client, notifications: notifications}
}

func (t *YouTubeSearchTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "search_youtube",
		Description: "Search YouTube for videos and display them in a floating player panel. Use this whenever the user asks for videos, songs, clips, DJ sets, tutorials, etc. The panel appears automatically with inline-playable results — do NOT paste the URLs into your text reply. Just briefly confirm in prose (e.g. 'Here are some sets I found').",
		Parameters: service.Parameters{
			{Name: "query", Type: service.ParamString, Description: "The search query (e.g. 'Boiler Room techno 2026')", Required: true},
			{Name: "num_results", Type: service.ParamInteger, Description: "Number of videos to return (default 6, max 12)", Required: false},
		}.ToJSONSchema(),
	}
}

// findingItem mirrors the structure consumed by the FloatingFindings panel.
// search_youtube pushes a `findings` notification so the existing link UI can
// render video results; the panel detects YouTube URLs and plays the active
// one inline.
type findingItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary,omitempty"`
	Source  string `json:"source,omitempty"`
}

type findingsSpec struct {
	Title string        `json:"title"`
	Items []findingItem `json:"items"`
}

const (
	youtubeDefaultResults = 6
	youtubeMaxResults     = 12
)

func (t *YouTubeSearchTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	query := call.Arguments.String("query", "")
	if query == "" {
		return tools.Failure(call.ID, tools.Validation("search_youtube", "query is required")), nil
	}

	limit := youtubeDefaultResults
	if n := call.Arguments.Int("num_results", 0); n > 0 {
		limit = n
	}
	if limit > youtubeMaxResults {
		limit = youtubeMaxResults
	}

	// Over-fetch so we have enough after filtering to YouTube-only.
	results, err := t.client.SearchVideos(ctx, query, limit*3)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("search_youtube", fmt.Errorf("youtube search: %w", err))), nil
	}

	items := make([]findingItem, 0, limit)
	for _, r := range results {
		// Filter to YouTube-hosted results so the inline player can render them.
		if extractYouTubeID(r.URL) == "" {
			continue
		}
		items = append(items, findingItem{
			Title:   r.Title,
			URL:     r.URL,
			Summary: r.Content,
			Source:  "youtube",
		})
		if len(items) >= limit {
			break
		}
	}

	if len(items) == 0 {
		return tools.Success(call.ID, "No YouTube results found."), nil
	}

	sessionID := service.SessionIDFromCtx(ctx)
	if t.notifications != nil && sessionID != "" {
		payload, err := json.Marshal(findingsSpec{Title: query, Items: items})
		if err != nil {
			return tools.Failure(call.ID, tools.Transient("search_youtube", fmt.Errorf("marshal findings payload: %w", err))), nil
		}
		t.notifications.Push(znotify.Notification{
			SessionID: sessionID,
			ToolName:  "findings",
			Content:   string(payload),
			Broadcast: true,
		})
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d YouTube results for %q:\n", len(items), query)
	for i, it := range items {
		fmt.Fprintf(&sb, "%d. %s — %s\n", i+1, it.Title, it.URL)
	}
	return tools.Success(call.ID, strings.TrimRight(sb.String(), "\n")), nil
}

var youtubeHostRe = regexp.MustCompile(`(?i)(^|\.)(youtube\.com|youtu\.be)$`)
var youtubeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// extractYouTubeID pulls the 11-character videoId out of any common YouTube
// URL shape. Returns "" if the URL isn't a recognisable YouTube video link.
func extractYouTubeID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Host)
	if !youtubeHostRe.MatchString(host) {
		return ""
	}
	// youtu.be/<id>
	if strings.HasSuffix(host, "youtu.be") {
		id := strings.TrimPrefix(u.Path, "/")
		if i := strings.Index(id, "/"); i >= 0 {
			id = id[:i]
		}
		if youtubeIDRe.MatchString(id) {
			return id
		}
		return ""
	}
	// youtube.com/watch?v=<id>
	if v := u.Query().Get("v"); youtubeIDRe.MatchString(v) {
		return v
	}
	// youtube.com/embed/<id>, /shorts/<id>, /live/<id>, /v/<id>
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) >= 2 {
		switch parts[0] {
		case "embed", "shorts", "live", "v":
			if youtubeIDRe.MatchString(parts[1]) {
				return parts[1]
			}
		}
	}
	return ""
}
