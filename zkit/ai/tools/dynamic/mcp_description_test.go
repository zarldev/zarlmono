package dynamic_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestMCPDescriptionLimitOverHTTP(t *testing.T) {
	const limit = 10 * 1024 * 1024
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			for _, size := range []int{2049, limit, limit + 1} {
				t.Run(strconv.Itoa(size), func(t *testing.T) {
					description := strings.Repeat("x", size)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var request struct {
							ID     json.RawMessage `json:"id"`
							Method string          `json:"method"`
						}
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Errorf("decode discovery request: %v", err)
							return
						}
						if request.Method == "server/discover" {
							w.Header().Set("Content-Type", "application/json")
							_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"resultType": "complete", "supportedVersions": []string{"2026-07-28"}, "capabilities": map[string]any{"tools": map[string]any{}}}})
							return
						}
						response, err := json.Marshal(map[string]any{
							"jsonrpc": "2.0", "id": request.ID,
							"result": map[string]any{"resultType": "complete", "tools": []mcp.ToolDef{{
								Name: "inspect_scene", Description: description,
								InputSchema: map[string]any{"type": "object"},
							}}},
						})
						if err != nil {
							t.Errorf("encode discovery response: %v", err)
							return
						}
						w.Header().Set("Content-Type", contentType)
						if contentType == "text/event-stream" {
							_, _ = fmt.Fprintf(w, "data: %s\n\n", response)
						} else {
							_, _ = w.Write(response)
						}
					}))
					t.Cleanup(server.Close)
					reg := tools.NewRegistry()
					connections := dynamic.NewMCPRegistry(reg, nil)
					t.Cleanup(func() { _ = connections.CloseAll() })
					connections.SetConnectPolicy(dynamic.NewMCPAllowlistPolicy(map[string]dynamic.MCPConnSpec{
						"blender": dynamic.NewHTTPMCPConn(server.URL, ""),
					}))
					result, err := dynamic.NewMCPConnect(connections).Execute(t.Context(), tools.ToolCall{
						ID: "connect", Arguments: tools.ToolParameters{
							"name": "blender", "transport": dynamic.Transports.TRANSPORTHTTP.String(), "base_url": server.URL,
						},
					})
					if err != nil {
						t.Fatalf("connect: %v", err)
					}
					tool, registered := reg.Tool("inspect_scene")
					if size > limit {
						if result.Success || registered || !strings.Contains(result.Error, "description too large") {
							t.Fatalf("over-limit description: result=%+v registered=%v", result, registered)
						}
						return
					}
					if !result.Success || !registered {
						t.Fatalf("description within limit: result=%+v registered=%v", result, registered)
					}
					if !strings.HasSuffix(tool.Definition().Description, description) {
						t.Fatal("description was truncated or changed")
					}
				})
			}
		})
	}
}
