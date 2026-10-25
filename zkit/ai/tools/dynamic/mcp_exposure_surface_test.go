package dynamic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestMCPDeferredLargeCatalogueSurface(t *testing.T) {
	host := tools.NewRegistry()
	connections := dynamic.NewMCPRegistry(host, nil)
	t.Cleanup(func() { _ = connections.CloseAll() })
	for serverIndex := range 4 {
		server := mcp.NewServer("catalogue", "1")
		for toolIndex := range 64 {
			server.RegisterTool(mcp.ToolDef{
				Name:        fmt.Sprintf("server_%d_tool_%02d", serverIndex, toolIndex),
				Description: "Fixture remote operation with a representative input schema",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
			}, func(context.Context, json.RawMessage) (mcp.CallResult, error) {
				return mcp.CallResult{}, nil
			})
		}
		httpServer := httptest.NewServer(server.Handler())
		t.Cleanup(httpServer.Close)
		name := fmt.Sprintf("fixture_%d", serverIndex)
		connections.SetConnectPolicy(dynamic.NewMCPAllowlistPolicy(map[string]dynamic.MCPConnSpec{
			name: dynamic.NewHTTPMCPConn(httpServer.URL, ""),
		}))
		result, err := dynamic.NewMCPConnect(connections).Execute(t.Context(), tools.ToolCall{
			ID: tools.ToolCallID(name), Arguments: tools.ToolParameters{"name": name, "transport": "http", "base_url": httpServer.URL},
		})
		if err != nil || !result.Success {
			t.Fatalf("connect %s: result=%+v err=%v", name, result, err)
		}
	}
	exposure := dynamic.NewMCPExposure(connections)
	if got := len(collectTools(t, exposure.Source())); got != 0 {
		t.Fatalf("unselected remote schemas = %d", got)
	}
	for _, match := range searchExposure(t, exposure, "server_0_tool", 2) {
		if result := activateExposure(t, exposure, match.Ref); !result.Success {
			t.Fatalf("activation: %+v", result)
		}
	}
	direct := collectTools(t, host)
	deferred := collectTools(t, exposure.Source())
	if len(direct) != 256 || len(deferred) != 2 {
		t.Fatalf("schema counts: direct=%d deferred=%d", len(direct), len(deferred))
	}
	encodedSurface := func(surface []tools.Tool) []byte {
		definitions := make([]tools.ToolSpec, 0, len(surface))
		for _, tool := range surface {
			definitions = append(definitions, tool.Definition())
		}
		encoded, err := json.Marshal(definitions)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	directBytes, deferredBytes := len(encodedSurface(direct)), len(encodedSurface(deferred))
	if deferredBytes >= directBytes {
		t.Fatalf("deferred schema bytes %d >= direct %d", deferredBytes, directBytes)
	}
	t.Logf("remote definition JSON bytes: direct(256)=%d deferred(2)=%d; excludes fixed host/control definitions", directBytes, deferredBytes)
}
