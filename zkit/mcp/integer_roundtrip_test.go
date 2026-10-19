package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestToolArgumentsPreserveExactIntegersAcrossServerDispatch(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer("integer-fixture", "1")
	server.RegisterTool(mcp.ToolDef{Name: "echo", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, args json.RawMessage) (mcp.CallResult, error) {
		return mcp.CallResult{Content: []mcp.Content{mcp.TextContent{Text: string(args)}}}, nil
	})
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	client := mcp.NewClient(httpServer.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	const integer int64 = 9007199254740993
	result, err := client.Call(t.Context(), "echo", map[string]any{"exact": integer})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "9007199254740993") {
		t.Fatal("server rounded tool arguments before execution")
	}
}

func TestServerRejectsInvalidJSONRPCBeforeToolExecution(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer("fixture", "1")
	server.RegisterTool(mcp.ToolDef{Name: "mutate"}, func(context.Context, json.RawMessage) (mcp.CallResult, error) {
		t.Error("invalid request executed a tool")
		return mcp.CallResult{}, nil
	})
	for _, body := range []string{`{"id":1,"method":"tools/call","params":{"name":"mutate"}}`, `{"jsonrpc":"1.0","id":1,"method":"tools/call","params":{"name":"mutate"}}`} {
		request := httptest.NewRequest(http.MethodPost, "http://fixture/mcp", strings.NewReader(body))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		var result struct {
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Error.Code != -32600 {
			t.Fatalf("invalid request not rejected: %s", response.Body.String())
		}
	}
}
