package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestSchemaHeadersDirectCallLoadsDefinitionsAndMirrorsArguments(t *testing.T) {
	var listCalls atomic.Int64
	var toolCalls atomic.Int64
	server := mcp.NewServer("headers", "1")
	server.RegisterTool(mcp.ToolDef{
		Name: "mirror",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message":  map[string]any{"type": "string", "x-mcp-header": "Message"},
				"enabled":  map[string]any{"type": "boolean", "x-mcp-header": "Enabled"},
				"count":    map[string]any{"type": "integer", "x-mcp-header": "Count"},
				"optional": map[string]any{"type": "string", "x-mcp-header": "Optional"},
				"absent":   map[string]any{"type": "string", "x-mcp-header": "Absent"},
				"nested": map[string]any{"type": "object", "properties": map[string]any{
					"label": map[string]any{"type": "string", "x-mcp-header": "Nested-Label"},
				}},
			},
		},
	}, func(_ context.Context, args json.RawMessage) (mcp.CallResult, error) {
		toolCalls.Add(1)
		if !strings.Contains(string(args), `9007199254740991`) {
			t.Errorf("integer argument lost precision: %s", args)
		}
		return mcp.CallResult{Content: []mcp.Content{mcp.TextContent{Text: "ok"}}}, nil
	})

	handler := server.Handler()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Method") == "tools/list" {
			listCalls.Add(1)
		}
		if r.Header.Get("Mcp-Method") == "tools/call" {
			if got := r.Header.Get("Mcp-Param-Message"); !strings.HasPrefix(got, "=?base64?") {
				t.Errorf("unsafe string header was not encoded: %q", got)
			}
			if got := r.Header.Get("Mcp-Param-Enabled"); got != "true" {
				t.Errorf("boolean header = %q", got)
			}
			if got := r.Header.Get("Mcp-Param-Count"); got != "9007199254740991" {
				t.Errorf("integer header = %q", got)
			}
			if got := r.Header.Get("Mcp-Param-Nested-Label"); got != "nested" {
				t.Errorf("nested header = %q", got)
			}
			if values := r.Header.Values("Mcp-Param-Optional"); len(values) != 0 {
				t.Errorf("null argument emitted a header: %q", values)
			}
			if values := r.Header.Values("Mcp-Param-Absent"); len(values) != 0 {
				t.Errorf("absent argument emitted a header: %q", values)
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(fixture.Close)

	client := mcp.NewClient(fixture.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	result, err := client.Call(t.Context(), "mirror", map[string]any{
		"message":  " leading ünicode ",
		"enabled":  true,
		"count":    json.Number("9007199254740991"),
		"optional": nil,
		"nested":   map[string]any{"label": "nested"},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.FirstText() != "ok" {
		t.Fatalf("result = %q", result.FirstText())
	}
	if listCalls.Load() != 1 {
		t.Fatalf("tools/list calls = %d, want 1", listCalls.Load())
	}
	if toolCalls.Load() != 1 {
		t.Fatalf("tool calls = %d, want 1", toolCalls.Load())
	}
}

func TestDiscoverExcludesOnlyMalformedSchemaHeaderTools(t *testing.T) {
	server := mcp.NewServer("headers", "1")
	noop := func(context.Context, json.RawMessage) (mcp.CallResult, error) { return mcp.CallResult{}, nil }
	server.RegisterTool(mcp.ToolDef{Name: "good", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string", "x-mcp-header": "Valid"}},
	}}, noop)
	server.RegisterTool(mcp.ToolDef{Name: "ordinary", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}},
	}}, noop)
	malformed := []mcp.ToolDef{
		{Name: "bad-token", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"v": map[string]any{"type": "string", "x-mcp-header": "bad header"}}}},
		{Name: "duplicate", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string", "x-mcp-header": "Same"}, "b": map[string]any{"type": "boolean", "x-mcp-header": "same"}}}},
		{Name: "array", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"v": map[string]any{"type": "array", "items": map[string]any{"type": "string", "x-mcp-header": "Array"}}}}},
		{Name: "composition", InputSchema: map[string]any{"type": "object", "allOf": []any{map[string]any{"properties": map[string]any{"v": map[string]any{"type": "string", "x-mcp-header": "Composed"}}}}}},
		{Name: "reference", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"v": map[string]any{"$ref": "#/$defs/v", "x-mcp-header": "Reference"}}}},
		{Name: "conditional", InputSchema: map[string]any{"type": "object", "if": map[string]any{"properties": map[string]any{"v": map[string]any{"type": "string", "x-mcp-header": "Conditional"}}}}},
	}
	for _, definition := range malformed {
		server.RegisterTool(definition, noop)
	}

	fixture := httptest.NewServer(server.Handler())
	t.Cleanup(fixture.Close)
	client := mcp.NewClient(fixture.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	tools, err := client.Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "good" || tools[1].Name != "ordinary" {
		t.Fatalf("discovered tools = %#v", tools)
	}
}

func TestSchemaHeaderMismatchStopsToolInvocation(t *testing.T) {
	var invoked atomic.Bool
	server := mcp.NewServer("headers", "1")
	server.RegisterTool(mcp.ToolDef{Name: "guarded", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string", "x-mcp-header": "Value"}},
	}}, func(context.Context, json.RawMessage) (mcp.CallResult, error) {
		invoked.Store(true)
		return mcp.CallResult{}, nil
	})

	handler := server.Handler()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Method") == "tools/call" {
			r.Header.Set("Mcp-Param-Value", "different")
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(fixture.Close)
	client := mcp.NewClient(fixture.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.Call(t.Context(), "guarded", map[string]any{"value": "expected"})
	if err == nil || !strings.Contains(err.Error(), "parameter header mismatch") {
		t.Fatalf("Call error = %v", err)
	}
	if invoked.Load() {
		t.Fatal("tool handler invoked after header mismatch")
	}
}

func TestSchemaHeaderRejectsUnsafeIntegerBeforeToolCall(t *testing.T) {
	var invoked atomic.Bool
	server := mcp.NewServer("headers", "1")
	server.RegisterTool(mcp.ToolDef{Name: "bounded", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"value": map[string]any{"type": "integer", "x-mcp-header": "Value"}},
	}}, func(context.Context, json.RawMessage) (mcp.CallResult, error) {
		invoked.Store(true)
		return mcp.CallResult{}, nil
	})
	fixture := httptest.NewServer(server.Handler())
	t.Cleanup(fixture.Close)
	client := mcp.NewClient(fixture.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.Call(t.Context(), "bounded", map[string]any{"value": json.Number("9007199254740992")})
	if err == nil || !strings.Contains(err.Error(), "safe range") {
		t.Fatalf("Call error = %v", err)
	}
	if invoked.Load() {
		t.Fatal("tool handler invoked with unsafe integer")
	}
}
