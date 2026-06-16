package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/service"
)

// ChartPoint is a single (x, y) sample on a line chart series. X is rendered as
// a string label (timestamp, date, category) to keep the wire format simple.
type ChartPoint struct {
	X string  `json:"x"`
	Y float64 `json:"y"`
}

// ChartSeries groups points under a named line.
type ChartSeries struct {
	Name   string       `json:"name"`
	Points []ChartPoint `json:"points"`
}

// ChartSpec is what the frontend renders. Kept minimal on purpose; add fields
// only when a real use case needs them.
type ChartSpec struct {
	Title  string `json:"title"`
	XLabel string `json:"x_label,omitempty"`
	YLabel string `json:"y_label,omitempty"`
	// YMin / YMax force the y-axis range. If both are zero, the axis
	// auto-scales to [dataMin, dataMax] with a small padding. Use these to
	// keep a stock chart tight around its price range instead of starting at 0.
	YMin *float64 `json:"y_min,omitempty"`
	YMax *float64 `json:"y_max,omitempty"`
	// YZeroBased forces the y-axis to start at 0 (the Recharts default).
	// Only set this when the series actually spans to/near zero and the
	// absolute scale matters (e.g. counts, volumes).
	YZeroBased bool          `json:"y_zero_based,omitempty"`
	Series     []ChartSeries `json:"series"`
}

// RenderChartTool pushes a chart spec to the frontend of the current session.
// The actual rendering happens client-side in the chat transcript.
type RenderChartTool struct {
	notifications *znotify.NotificationStore

	mu       sync.Mutex
	lastSent map[string]chartCache // keyed by session ID
}

// chartCache guards against models that call render_chart twice with identical
// args in the same turn (common when the model doesn't trust its own tool output).
type chartCache struct {
	hash string
	at   time.Time
}

const chartDedupeWindow = 30 * time.Second

func NewRenderChartTool(notifications *znotify.NotificationStore) *RenderChartTool {
	return &RenderChartTool{
		notifications: notifications,
		lastSent:      make(map[string]chartCache),
	}
}

func (t *RenderChartTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "render_chart",
		Description: "Render a line chart in the conversation. Call this tool with structured arguments — the chart appears inline in the chat automatically. Do NOT embed <chart> tags, markdown image syntax, or any other markup in your text reply; the tool call is the entire rendering mechanism. After the tool returns, just briefly confirm in plain prose (e.g. 'Here's the chart'). Use for numeric series over time (stock prices, crypto prices, sensor readings). Pass a title and one or more series, each with an array of {x, y} points. X is a label (timestamp, date, category); Y is numeric. The y-axis auto-fits the data range by default — do NOT set y_zero_based unless the absolute scale matters (volumes, counts). For prices, leave y_zero_based off so the chart zooms into the range.",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":   map[string]any{"type": "string", "description": "Chart title"},
				"x_label": map[string]any{"type": "string", "description": "X-axis label (optional)"},
				"y_label": map[string]any{"type": "string", "description": "Y-axis label (optional)"},
				"y_min":   map[string]any{"type": "number", "description": "Force y-axis minimum (optional). Omit for auto-fit."},
				"y_max":   map[string]any{"type": "number", "description": "Force y-axis maximum (optional). Omit for auto-fit."},
				"y_zero_based": map[string]any{
					"type":        "boolean",
					"description": "Force y-axis to start at 0. Default false (auto-fit). Only set true for volumes/counts where absolute scale matters.",
				},
				"series": map[string]any{
					"type":        "array",
					"description": "One or more named lines",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
							"points": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"x": map[string]any{"type": "string"},
										"y": map[string]any{"type": "number"},
									},
									"required": []string{"x", "y"},
								},
							},
						},
						"required": []string{"name", "points"},
					},
				},
			},
			"required": []string{"title", "series"},
		}),
	}
}

func (t *RenderChartTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	sessionID := service.SessionIDFromCtx(ctx)
	if sessionID == "" {
		return tools.Failure(call.ID, tools.Validation("render_chart", "no session in context")), nil
	}

	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("render_chart", err)), nil
	}
	var spec ChartSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return tools.Failure(call.ID, tools.Validation("render_chart", fmt.Sprintf("decode chart spec: %v", err))), nil
	}
	if spec.Title == "" || len(spec.Series) == 0 {
		return tools.Failure(call.ID, tools.Validation("render_chart", "title and at least one series are required")), nil
	}
	for i, s := range spec.Series {
		if len(s.Points) == 0 {
			return tools.Failure(call.ID, tools.Validation("render_chart", fmt.Sprintf("series %d %q has no points", i, s.Name))), nil
		}
	}

	payload, err := json.Marshal(spec)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("render_chart", err)), nil
	}

	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])

	t.mu.Lock()
	last, ok := t.lastSent[sessionID]
	if ok && last.hash == hash && time.Since(last.at) < chartDedupeWindow {
		t.mu.Unlock()
		return tools.Success(call.ID, fmt.Sprintf("Chart %q was already rendered. Do not call render_chart again for this request — reply in plain text instead.", spec.Title)), nil
	}
	t.lastSent[sessionID] = chartCache{hash: hash, at: time.Now()}
	t.mu.Unlock()

	t.notifications.Push(znotify.Notification{
		SessionID: sessionID,
		ToolName:  "chart",
		Content:   string(payload),
		Broadcast: true,
	})

	return tools.Success(call.ID, fmt.Sprintf("Chart %q rendered in the chat. Do not call render_chart again — confirm the chart briefly in plain text.", spec.Title)), nil
}
