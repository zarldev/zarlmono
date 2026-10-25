package dynamic_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

type exposureServer struct {
	server      *httptest.Server
	description atomic.Value
	callStarted chan struct{}
	blockCalls  bool
	startedOnce sync.Once
}

func newExposureServer(t *testing.T, description string, blockCalls bool) *exposureServer {
	t.Helper()
	fixture := &exposureServer{description: atomic.Value{}, callStarted: make(chan struct{}), blockCalls: blockCalls}
	fixture.description.Store(description)
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		var result any
		switch request.Method {
		case "server/discover":
			result = map[string]any{"resultType": "complete", "supportedVersions": []string{"2026-07-28"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "inspect_scene", "description": fixture.description.Load().(string),
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			}}}
		case "tools/call":
			fixture.startedOnce.Do(func() { close(fixture.callStarted) })
			if fixture.blockCalls {
				<-r.Context().Done()
				return
			}
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
		default:
			result = map[string]any{}
		}
		if object, ok := result.(map[string]any); ok {
			object["resultType"] = "complete"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func newExposure(t *testing.T, server *exposureServer, reserved ...tools.ToolName) (*dynamic.MCPRegistry, *dynamic.MCPExposure) {
	const connection = "blender"
	t.Helper()
	registry := tools.NewRegistry()
	connections := dynamic.NewMCPRegistry(registry, nil)
	t.Cleanup(func() { _ = connections.CloseAll() })
	exposure := dynamic.NewMCPExposure(connections, reserved...)
	spec := dynamic.NewHTTPMCPConn(server.server.URL, "")
	connections.SetConnectPolicy(dynamic.NewMCPAllowlistPolicy(map[string]dynamic.MCPConnSpec{connection: spec}))
	result, err := dynamic.NewMCPConnect(connections).Execute(t.Context(), tools.ToolCall{
		ID: "connect", Arguments: tools.ToolParameters{
			"name": connection, "transport": dynamic.Transports.TRANSPORTHTTP.String(), "base_url": server.server.URL,
		},
	})
	if err != nil || !result.Success {
		t.Fatalf("connect: result=%+v err=%v", result, err)
	}
	if _, direct := registry.Tool("inspect_scene"); !direct {
		t.Fatal("constructing an exposure changed direct registry behavior")
	}
	dynamic.NewMCPExposure(connections)
	if _, direct := registry.Tool("inspect_scene"); !direct {
		t.Fatal("constructing a later exposure unregistered a direct tool")
	}
	return connections, exposure
}

func exposureControl(t *testing.T, exposure *dynamic.MCPExposure, name tools.ToolName) tools.Tool {
	t.Helper()
	for _, tool := range exposure.ControlTools() {
		if tool.Definition().Name == name {
			return tool
		}
	}
	t.Fatalf("control tool %q not found", name)
	return nil
}

func searchExposure(t *testing.T, exposure *dynamic.MCPExposure, query string, limit int) []dynamic.MCPSearchResult {
	t.Helper()
	result, err := exposureControl(t, exposure, dynamic.ToolNameMCPSearch).Execute(t.Context(), tools.ToolCall{
		ID: "search", Arguments: tools.ToolParameters{"query": query, "limit": limit},
	})
	if err != nil || !result.Success {
		t.Fatalf("search: result=%+v err=%v", result, err)
	}
	matches, ok := result.Data.([]dynamic.MCPSearchResult)
	if !ok {
		t.Fatalf("search data type = %T", result.Data)
	}
	return matches
}

func activateExposure(t *testing.T, exposure *dynamic.MCPExposure, ref dynamic.MCPToolRef) *tools.ToolResult {
	t.Helper()
	result, err := exposureControl(t, exposure, dynamic.ToolNameMCPActivate).Execute(t.Context(), tools.ToolCall{
		ID: "activate", Arguments: tools.ToolParameters{
			"connection": ref.Connection, "name": ref.Name, "definition_hash": ref.DefinitionHash,
		},
	})
	if err != nil {
		t.Fatalf("activate transport error: %v", err)
	}
	return result
}

func collectTools(t *testing.T, source tools.Source) []tools.Tool {
	t.Helper()
	return slices.Collect(source.Tools(t.Context()))
}

func TestMCPExposureSearchActivateExecuteAndDefinitionChange(t *testing.T) {
	server := newExposureServer(t, strings.Repeat("界", 300), false)
	connections, exposure := newExposure(t, server)

	matches := searchExposure(t, exposure, "inspect", 10)
	if len(matches) != 1 {
		t.Fatalf("search matches = %d, want 1", len(matches))
	}
	if got := len([]rune(matches[0].Description)); got != 240 {
		t.Fatalf("description runes = %d, want 240", got)
	}
	oldRef := matches[0].Ref
	if result := activateExposure(t, exposure, oldRef); !result.Success {
		t.Fatalf("activate: %+v", result)
	}
	if got := exposure.Snapshot(); !slices.Equal(got, []dynamic.MCPToolRef{oldRef}) {
		t.Fatalf("snapshot = %+v", got)
	}
	visible := collectTools(t, exposure.Source())
	if len(visible) != 1 || visible[0].Definition().Name != "inspect_scene" {
		t.Fatalf("visible tools = %+v", visible)
	}
	result, err := exposure.Source().Execute(t.Context(), tools.ToolCall{ID: "call", ToolName: "inspect_scene"})
	if err != nil || !result.Success {
		t.Fatalf("execute: result=%+v err=%v", result, err)
	}

	if _, err := dynamic.NewMCPDisconnect(connections).Execute(t.Context(), tools.ToolCall{
		ID: "disconnect", Arguments: tools.ToolParameters{"name": "blender"},
	}); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	server.description.Store("changed definition")
	spec := dynamic.NewHTTPMCPConn(server.server.URL, "")
	connections.SetConnectPolicy(dynamic.NewMCPAllowlistPolicy(map[string]dynamic.MCPConnSpec{"blender": spec}))
	connectResult, err := dynamic.NewMCPConnect(connections).Execute(t.Context(), tools.ToolCall{
		ID: "reconnect", Arguments: tools.ToolParameters{
			"name": "blender", "transport": dynamic.Transports.TRANSPORTHTTP.String(), "base_url": server.server.URL,
		},
	})
	if err != nil || !connectResult.Success {
		t.Fatalf("reconnect: result=%+v err=%v", connectResult, err)
	}
	newRef := searchExposure(t, exposure, "inspect", 10)[0].Ref
	if newRef.DefinitionHash == oldRef.DefinitionHash {
		t.Fatal("description change did not change definition hash")
	}
	if got := collectTools(t, exposure.Source()); len(got) != 0 {
		t.Fatalf("stale desired reference exposed %d tools", len(got))
	}
	if _, err := exposure.Source().Execute(t.Context(), tools.ToolCall{ID: "stale", ToolName: "inspect_scene"}); tools.KindOf(err) != tools.Kinds.NOTFOUND {
		t.Fatalf("stale execute error = %v, want NOTFOUND", err)
	}
}
func TestMCPExposureReplaceValidationAndReservedCollision(t *testing.T) {
	server := newExposureServer(t, "scene inspection", false)
	_, exposure := newExposure(t, server, "inspect_scene")
	ref := searchExposure(t, exposure, "", 0)[0].Ref
	if result := activateExposure(t, exposure, ref); result.Success || tools.KindOf(result.Err) != tools.Kinds.PERMISSION {
		t.Fatalf("reserved activation = %+v", result)
	}

	dormant := ref
	dormant.Connection = "future"
	if err := exposure.Replace([]dynamic.MCPToolRef{dormant}); err != nil {
		t.Fatalf("Replace dormant: %v", err)
	}
	if got := exposure.Snapshot(); !slices.Equal(got, []dynamic.MCPToolRef{dormant}) {
		t.Fatalf("dormant snapshot = %+v", got)
	}
	tooMany := make([]dynamic.MCPToolRef, 17)
	for i := range tooMany {
		tooMany[i] = dormant
		tooMany[i].Connection = "server_" + string(rune('a'+i))
	}
	if err := exposure.Replace(tooMany); tools.KindOf(err) != tools.Kinds.BUDGET {
		t.Fatalf("Replace over cap error = %v, want BUDGET", err)
	}
	if got := exposure.Snapshot(); !slices.Equal(got, []dynamic.MCPToolRef{dormant}) {
		t.Fatalf("failed Replace mutated snapshot = %+v", got)
	}
}
func TestMCPExposureReserveInvalidatesEnumeratedTool(t *testing.T) {
	server := newExposureServer(t, "scene inspection", false)
	connections, exposure := newExposure(t, server)
	ref := searchExposure(t, exposure, "", 0)[0].Ref
	if result := activateExposure(t, exposure, ref); !result.Success {
		t.Fatalf("activate: %+v", result)
	}
	visible := collectTools(t, exposure.Source())
	if len(visible) != 1 {
		t.Fatalf("visible tools = %d, want 1", len(visible))
	}

	exposure.Reserve(ref.Name)
	if got := collectTools(t, exposure.Source()); len(got) != 0 {
		t.Fatalf("reserved exposure contains %d tools", len(got))
	}
	if _, err := visible[0].Execute(t.Context(), tools.ToolCall{ID: "stale", ToolName: ref.Name}); tools.KindOf(err) != tools.Kinds.NOTFOUND {
		t.Fatalf("enumerated tool after Reserve error = %v, want NOTFOUND", err)
	}
	if got := exposure.Snapshot(); !slices.Equal(got, []dynamic.MCPToolRef{ref}) {
		t.Fatalf("Reserve discarded dormant selection: %+v", got)
	}

	child := dynamic.NewMCPExposure(connections)
	if err := child.Replace(exposure.Snapshot()); err != nil {
		t.Fatalf("child Replace: %v", err)
	}
	if got := collectTools(t, child.Source()); len(got) != 1 {
		t.Fatalf("independent child visible tools = %d, want 1", len(got))
	}
}
func TestMCPDisconnectCancelsAndDrainsGenerationBoundCall(t *testing.T) {
	server := newExposureServer(t, "blocking", true)
	connections, exposure := newExposure(t, server)
	ref := searchExposure(t, exposure, "", 10)[0].Ref
	if result := activateExposure(t, exposure, ref); !result.Success {
		t.Fatalf("activate: %+v", result)
	}
	visible := collectTools(t, exposure.Source())
	if len(visible) != 1 {
		t.Fatalf("visible tools = %d", len(visible))
	}
	ctx := t.Context()
	callDone := make(chan error, 1)
	go func() {
		_, err := visible[0].Execute(ctx, tools.ToolCall{ID: "blocked", ToolName: "inspect_scene"})
		callDone <- err
	}()
	<-server.callStarted

	disconnectDone := make(chan error, 1)
	go func() {
		result, err := dynamic.NewMCPDisconnect(connections).Execute(ctx, tools.ToolCall{
			ID: "disconnect", Arguments: tools.ToolParameters{"name": "blender"},
		})
		if err == nil && !result.Success {
			err = errors.New(result.Error)
		}
		disconnectDone <- err
	}()
	if err := <-callDone; err == nil {
		t.Fatal("blocked call completed without cancellation error")
	}
	if err := <-disconnectDone; err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if _, err := visible[0].Execute(t.Context(), tools.ToolCall{ID: "old", ToolName: "inspect_scene"}); tools.KindOf(err) != tools.Kinds.NOTFOUND {
		t.Fatalf("old generation execute error = %v, want NOTFOUND", err)
	}
}
