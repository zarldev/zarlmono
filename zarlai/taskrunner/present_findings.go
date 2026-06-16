package taskrunner

import (
	"context"
	"encoding/json"
	"fmt"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// PresentFindingsTool lets a background task push a rich, visual summary of
// findings (ranked links, videos, articles) to the user's frontend. Parallels
// render_chart — the structured payload is rendered as a floating panel in the
// immersive UI; the tool call is the entire display mechanism.
type PresentFindingsTool struct {
	notifications *znotify.NotificationStore
}

func NewPresentFindingsTool(notifications *znotify.NotificationStore) *PresentFindingsTool {
	return &PresentFindingsTool{notifications: notifications}
}

func (t *PresentFindingsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "present_findings",
		Description: "Display a ranked list of links, videos, or articles as a floating panel in the user's UI. Use this whenever a research task produces concrete URLs the user should see (e.g. Boiler Room sets, product pages, articles). The panel appears automatically — do NOT paste the same URLs into your text reply. Keep report_progress for prose observations; use present_findings for the actual links. Each item must have a title and url; summary is a short 1-2 sentence blurb.",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{"type": "string", "description": "Panel heading (e.g. 'Recent Boiler Room sets')"},
				"items": map[string]any{
					"type":        "array",
					"description": "Ranked list of findings, best first",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":   map[string]any{"type": "string"},
							"url":     map[string]any{"type": "string"},
							"summary": map[string]any{"type": "string", "description": "1-2 sentence blurb"},
							"source":  map[string]any{"type": "string", "description": "Optional source name (youtube, soundcloud, nytimes, etc.)"},
						},
						"required": []string{"title", "url"},
					},
				},
			},
			"required": []string{"title", "items"},
		}),
	}
}

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

func (t *PresentFindingsTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	sessionID := service.SessionIDFromCtx(ctx)
	if sessionID == "" {
		return tools.Failure(call.ID, tools.Validation("present_findings", "no session in context")), nil
	}

	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("present_findings", fmt.Sprintf("marshal args: %v", err))), nil
	}
	var spec findingsSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return tools.Failure(call.ID, tools.Validation("present_findings", fmt.Sprintf("decode findings spec: %v", err))), nil
	}
	if spec.Title == "" || len(spec.Items) == 0 {
		return tools.Failure(call.ID, tools.Validation("present_findings", "title and at least one item are required")), nil
	}
	for i, it := range spec.Items {
		if it.Title == "" || it.URL == "" {
			return tools.Failure(call.ID, tools.Validation("present_findings", fmt.Sprintf("item %d: title and url are required", i))), nil
		}
	}

	payload, err := json.Marshal(spec)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("present_findings", fmt.Errorf("marshal findings spec: %w", err))), nil
	}

	t.notifications.Push(znotify.Notification{
		SessionID: sessionID,
		ToolName:  "findings",
		Content:   string(payload),
		Broadcast: true,
	})

	return tools.Success(call.ID, fmt.Sprintf("Presented %d findings under %q in the UI.", len(spec.Items), spec.Title)), nil
}
