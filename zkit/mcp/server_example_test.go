package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

// ExampleNewServer demonstrates registering a tool and calling it
// over an in-process server.
func ExampleNewServer() {
	srv := mcp.NewServer("example", "1.0")

	srv.RegisterTool(
		mcp.ToolDef{
			Name:        "shout",
			Description: "uppercase the input",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"text"},
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
			},
		},
		func(_ context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.CallResult{}, err
			}
			out := ""
			var outSb36 strings.Builder
			for _, r := range in.Text {
				if r >= 'a' && r <= 'z' {
					r = r - 'a' + 'A'
				}
				outSb36.WriteRune(r)
			}
			out += outSb36.String()
			return mcp.CallResult{
				Content: []mcp.Content{mcp.TextContent{Text: out}},
			}, nil
		},
	)

	// Real consumers would mount with http.Handle("/mcp", srv.Handler())
	// and call srv.ListenAndServe(":8080", "/mcp"). Notifications go
	// out via:
	//   srv.Notify("notifications/progress", map[string]any{"percent": 50})

	fmt.Println(srv.Tools()[0].Name)
	// Output: shout
}
