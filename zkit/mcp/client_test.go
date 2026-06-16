package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func fakeMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q, want Bearer test-token", r.Header.Get("Authorization"))
		}

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		method, _ := req["method"].(string)
		w.Header().Set("Content-Type", "application/json")

		switch method {
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "echo",
							"description": "Echoes the input back",
							"inputSchema": map[string]any{
								"properties": map[string]any{
									"message": map[string]any{
										"type":        "string",
										"description": "The message to echo",
									},
								},
								"required": []string{"message"},
							},
						},
					},
				},
			})
		case "tools/call":
			params, _ := req["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			msg, _ := args["message"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": msg},
					},
				},
			})
		default:
			t.Errorf("unknown method: %s", method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
}

func TestDiscover(t *testing.T) {
	t.Parallel()

	srv := fakeMCPServer(t)
	t.Cleanup(srv.Close)

	c := mcp.NewClient(srv.URL, "test-token")
	defs, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d, want 1", len(defs))
	}

	def := defs[0]
	if def.Name != "echo" {
		t.Errorf("name = %q, want echo", def.Name)
	}
	if def.Description != "Echoes the input back" {
		t.Errorf("description = %q", def.Description)
	}
	if def.InputSchema == nil {
		t.Fatalf("InputSchema is nil")
	}
	props, _ := def.InputSchema["properties"].(map[string]any)
	if _, ok := props["message"]; !ok {
		t.Errorf("InputSchema.properties missing message: %v", props)
	}
	req, _ := def.InputSchema["required"].([]any)
	if len(req) != 1 || req[0] != "message" {
		t.Errorf("InputSchema.required = %v, want [message]", req)
	}
}

func TestCall(t *testing.T) {
	t.Parallel()

	srv := fakeMCPServer(t)
	t.Cleanup(srv.Close)

	c := mcp.NewClient(srv.URL, "test-token")

	defs, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("no tools discovered")
	}

	got, err := c.Call(context.Background(), defs[0].Name, map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(got.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(got.Content))
	}
	text, ok := got.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", got.Content[0])
	}
	if text.Text != "hello" {
		t.Errorf("text = %q, want hello", text.Text)
	}
	if got.FirstText() != "hello" {
		t.Errorf("FirstText = %q, want hello", got.FirstText())
	}
}

func TestCallMultimodalContent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "Here's the rendered diagram:"},
					{"type": "image", "data": "iVBORw0K...", "mimeType": "image/png"},
					{"type": "resource", "uri": "file:///diagram.svg", "mimeType": "image/svg+xml"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := mcp.NewClient(srv.URL, "")
	got, err := c.Call(context.Background(), "render_diagram", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(got.Content) != 3 {
		t.Fatalf("content len = %d, want 3", len(got.Content))
	}

	if _, ok := got.Content[0].(mcp.TextContent); !ok {
		t.Errorf("Content[0] type = %T, want TextContent", got.Content[0])
	}
	img, ok := got.Content[1].(mcp.ImageContent)
	if !ok {
		t.Fatalf("Content[1] type = %T, want ImageContent", got.Content[1])
	}
	if img.MIMEType != "image/png" {
		t.Errorf("image MIMEType = %q, want image/png", img.MIMEType)
	}
	if img.Data != "iVBORw0K..." {
		t.Errorf("image Data = %q", img.Data)
	}
	res, ok := got.Content[2].(mcp.ResourceContent)
	if !ok {
		t.Fatalf("Content[2] type = %T, want ResourceContent", got.Content[2])
	}
	if res.URI != "file:///diagram.svg" {
		t.Errorf("resource URI = %q", res.URI)
	}
}

func TestCallIsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"isError": true,
				"content": []map[string]any{
					{"type": "text", "text": "tool said no"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := mcp.NewClient(srv.URL, "")
	got, err := c.Call(context.Background(), "doomed", nil)
	if err != nil {
		// Transport-level error is not how MCP signals tool failure.
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !got.IsError {
		t.Error("IsError = false, want true")
	}
	if got.FirstText() != "tool said no" {
		t.Errorf("error text = %q", got.FirstText())
	}
}

func TestReadResource(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "resources/read" {
			t.Fatalf("method = %v, want resources/read", req["method"])
		}
		params, _ := req["params"].(map[string]any)
		if params["uri"] != "file:///doc.txt" {
			t.Errorf("uri = %v", params["uri"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"contents": []map[string]any{
					{"type": "text", "text": "Hello from doc.txt"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := mcp.NewClient(srv.URL, "")
	got, err := c.ReadResource(context.Background(), "file:///doc.txt")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	text, ok := got[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("got = %T, want TextContent", got[0])
	}
	if text.Text != "Hello from doc.txt" {
		t.Errorf("text = %q", text.Text)
	}
}

func TestReadResource_Multimodal(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"contents": []map[string]any{
					{"type": "image", "data": "base64data", "mimeType": "image/png"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := mcp.NewClient(srv.URL, "")
	got, err := c.ReadResource(context.Background(), "file:///image.png")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	img, ok := got[0].(mcp.ImageContent)
	if !ok {
		t.Fatalf("got = %T, want ImageContent", got[0])
	}
	if img.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q", img.MIMEType)
	}
}

func TestCallSkipsUnknownContentType(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "future_format", "data": "..."},
					{"type": "text", "text": "hello"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := mcp.NewClient(srv.URL, "")
	got, _ := c.Call(context.Background(), "thing", nil)
	if len(got.Content) != 1 {
		t.Fatalf("expected unknown type to be skipped; got %d content items", len(got.Content))
	}
	if _, ok := got.Content[0].(mcp.TextContent); !ok {
		t.Errorf("surviving item type = %T, want TextContent", got.Content[0])
	}
}

func TestDiscoverDefaultsEmptyInputSchema(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"tools": []map[string]any{
					{"name": "no_input", "description": "takes no args"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := mcp.NewClient(srv.URL, "")
	defs, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("len = %d", len(defs))
	}
	if defs[0].InputSchema == nil {
		t.Fatal("InputSchema should be defaulted to non-nil empty object")
	}
	if defs[0].InputSchema["type"] != "object" {
		t.Errorf("InputSchema.type = %v, want object", defs[0].InputSchema["type"])
	}
}
