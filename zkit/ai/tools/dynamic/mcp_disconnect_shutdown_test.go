package dynamic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestMCPRegistryCloseJoinsConcurrentDisconnect(t *testing.T) {
	deleteStarted, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseDelete := func() { releaseOnce.Do(func() { close(release) }) }
	server := newDisconnectSessionServer(t, deleteStarted, release)
	defer releaseDelete()
	registry := tools.NewRegistry()
	connections := dynamic.NewMCPRegistry(registry, nil)
	defer func() { releaseDelete(); _ = connections.CloseAll() }()
	connect := func(url string) {
		t.Helper()
		connections.SetConnectPolicy(dynamic.NewMCPAllowlistPolicy(map[string]dynamic.MCPConnSpec{
			"peer": dynamic.NewHTTPMCPConn(url, ""),
		}))
		result, err := dynamic.NewMCPConnect(connections).Execute(t.Context(), tools.ToolCall{
			ID: "connect", Arguments: tools.ToolParameters{"name": "peer", "transport": "http", "base_url": url},
		})
		if err != nil || !result.Success {
			t.Fatalf("connect: %+v, %v", result, err)
		}
	}
	connect(server.URL)
	oldTool, ok := registry.Tool("inspect_scene")
	if !ok {
		t.Fatal("missing original tool")
	}
	disconnectDone := make(chan struct{})
	var disconnectResult *tools.ToolResult
	var disconnectErr error
	disconnectCtx := t.Context()
	go func() {
		defer close(disconnectDone)
		disconnectResult, disconnectErr = dynamic.NewMCPDisconnect(connections).Execute(disconnectCtx, tools.ToolCall{
			ID: "disconnect", Arguments: tools.ToolParameters{"name": "peer"},
		})
	}()
	defer func() { releaseDelete(); <-disconnectDone }()
	select {
	case <-deleteStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not reach session cleanup")
	}

	// A removed generation must stay revoked, while its name can be reused
	// without waiting for the old peer's transport cleanup.
	replacement := newExposureServer(t, "replacement", true)
	connect(replacement.server.URL)
	if _, err := oldTool.Execute(t.Context(), tools.ToolCall{ID: "old", ToolName: "inspect_scene"}); tools.KindOf(err) != tools.Kinds.NOTFOUND {
		t.Fatalf("old generation = %v, want NOTFOUND", err)
	}
	newTool, ok := registry.Tool("inspect_scene")
	if !ok {
		t.Fatal("missing replacement tool")
	}
	ctx, cancel := context.WithCancel(t.Context())
	callDone := make(chan struct{})
	var callErr error
	go func() {
		defer close(callDone)
		_, callErr = newTool.Execute(ctx, tools.ToolCall{ID: "live", ToolName: "inspect_scene"})
	}()
	defer func() { cancel(); <-callDone }()
	select {
	case <-replacement.callStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement call did not start")
	}

	closeResults := make(chan error, 2)
	var closers sync.WaitGroup
	for range 2 {
		closers.Go(func() { closeResults <- connections.CloseAll() })
	}
	defer func() { releaseDelete(); closers.Wait() }()
	select {
	case <-callDone:
		if callErr == nil {
			t.Fatal("replacement call completed without cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel replacement call before disconnect joined")
	}
	if _, exists := registry.Tool("inspect_scene"); exists {
		t.Fatal("replacement tool remains visible during shutdown")
	}
	select {
	case <-disconnectDone:
		t.Fatal("fixture disconnect finished before gate release")
	default:
	}
	select {
	case err := <-closeResults:
		t.Fatalf("CloseAll returned before concurrent disconnect cleanup: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-disconnectDone:
		t.Fatal("fixture disconnect timed out before gate release")
	default:
	}
	releaseDelete()
	<-disconnectDone
	if disconnectErr != nil || !disconnectResult.Success {
		t.Fatalf("disconnect: %+v, %v", disconnectResult, disconnectErr)
	}
	for range 2 {
		select {
		case err := <-closeResults:
			if err != nil {
				t.Errorf("CloseAll: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("CloseAll did not join completed disconnect")
		}
	}
	if err := connections.CloseAll(); err != nil {
		t.Fatalf("repeat CloseAll: %v", err)
	}
}

func newDisconnectSessionServer(t *testing.T, deleteStarted chan<- struct{}, release <-chan struct{}) *httptest.Server {
	t.Helper()
	var deleteOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteOnce.Do(func() { close(deleteStarted) })
			select {
			case <-release:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "server/discover":
			response["error"] = map[string]any{"code": -32601, "message": "method not found"}
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "disconnect-session")
			response["result"] = map[string]any{
				"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
				"serverInfo": map[string]any{"name": "peer", "version": "1"},
			}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			response["result"] = map[string]any{"tools": []map[string]any{{
				"name": "inspect_scene", "description": "original", "inputSchema": map[string]any{"type": "object"},
			}}}
		default:
			t.Errorf("unexpected MCP method %q", request.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return server
}
