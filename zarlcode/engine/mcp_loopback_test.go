package engine_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestConfiguredLoopbackMCPConnectsAndRegistersTools(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost"} {
		t.Run(host, func(t *testing.T) {
			server := mcp.NewServer("local-scene", "1")
			server.RegisterTool(mcp.ToolDef{
				Name: "inspect_scene", Description: "Read the scene name",
				InputSchema: map[string]any{"type": "object"},
			}, func(context.Context, json.RawMessage) (mcp.CallResult, error) {
				return mcp.CallResult{Content: []mcp.Content{mcp.TextContent{Text: "untouched scene"}}}, nil
			})
			httpServer := httptest.NewServer(server.Handler())
			t.Cleanup(httpServer.Close)
			endpoint := strings.Replace(httpServer.URL, "127.0.0.1", host, 1)
			spec := dynamic.NewHTTPMCPConn(endpoint, "")
			reg := tools.NewRegistry()
			connections := dynamic.NewMCPRegistry(reg, nil)
			t.Cleanup(func() { _ = connections.CloseAll() })
			connections.SetConnectPolicy(engine.NewMCPConnectPolicy(map[string]dynamic.MCPConnSpec{"blender_lab": spec}))

			result, err := dynamic.NewMCPConnect(connections).Execute(t.Context(), tools.ToolCall{
				ID: "connect", Arguments: tools.ToolParameters{
					"name": "blender_lab", "transport": "http", "base_url": endpoint,
				},
			})
			if err != nil || !result.Success {
				t.Fatalf("configured MCP connect: result=%+v err=%v", result, err)
			}
			listing, err := dynamic.NewMCPList(connections).Execute(t.Context(), tools.ToolCall{ID: "list"})
			listed, _ := tools.DataAs[string](listing)
			if err != nil || !listing.Success || !strings.Contains(listed, "blender_lab") {
				t.Fatalf("active connections: result=%+v err=%v", listing, err)
			}
			inspect, ok := reg.Tool("inspect_scene")
			if !ok {
				t.Fatal("discovered tool was not registered")
			}
			output, err := inspect.Execute(t.Context(), tools.ToolCall{ID: "inspect", Arguments: tools.ToolParameters{}})
			if err != nil || !output.Success {
				t.Fatalf("read-only MCP tool: result=%+v err=%v", output, err)
			}
			data, ok := tools.DataAs[map[string]any](output)
			if !ok || data["trusted"] != false {
				t.Fatal("MCP output lost its untrusted envelope")
			}
			content, ok := data["content"].([]mcp.Content)
			if !ok || len(content) != 1 || content[0] != (mcp.TextContent{Text: "untouched scene"}) {
				t.Fatalf("scene content = %#v", data["content"])
			}
		})
	}
}
