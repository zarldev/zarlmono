package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

// echoTool is a minimal tool that returns its input as text. Used by
// most tests in this file.
func echoTool() (mcp.ToolDef, mcp.ToolHandler) {
	def := mcp.ToolDef{
		Name:        "echo",
		Description: "echo back the text",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []string{"text"},
		},
	}
	handler := func(_ context.Context, args json.RawMessage) (mcp.CallResult, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.CallResult{}, err
		}
		return mcp.CallResult{
			Content: []mcp.Content{mcp.TextContent{Text: in.Text}},
		}, nil
	}
	return def, handler
}

func TestServer_RegisterAndList(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	def, handler := echoTool()
	s.RegisterTool(def, handler)

	got := s.Tools()
	if len(got) != 1 || got[0].Name != "echo" {
		t.Fatalf("Tools() = %v", got)
	}
}

func TestServer_UnregisterTool(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	def, handler := echoTool()
	s.RegisterTool(def, handler)

	if !s.UnregisterTool("echo") {
		t.Error("UnregisterTool returned false for known tool")
	}
	if s.UnregisterTool("echo") {
		t.Error("UnregisterTool returned true for already-removed tool")
	}
	if got := s.Tools(); len(got) != 0 {
		t.Errorf("Tools() after unregister = %v, want empty", got)
	}
}

func TestServer_HTTPRoundTrip_ToolsList(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	def, handler := echoTool()
	s.RegisterTool(def, handler)

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "")
	defs, err := client.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "echo" {
		t.Fatalf("Discover returned %v", defs)
	}
}

func TestServer_HTTPRoundTrip_ToolsCall(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	def, handler := echoTool()
	s.RegisterTool(def, handler)

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "")
	res, err := client.Call(context.Background(), "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := res.FirstText(); got != "hello" {
		t.Errorf("FirstText = %q, want hello", got)
	}
	if res.IsError {
		t.Error("IsError = true on successful call")
	}
}

// TestServer_HTTPRoundTrip_NotificationProducesNoResponse exercises
// the JSON-RPC §4.1 invariant: a request with no "id" member is a
// notification, and the server MUST NOT respond. Earlier the
// dispatcher echoed a synthetic ID 0 response for
// notifications/initialized, which violated the spec and confused
// clients that distinguish notifications by absence-of-id.
func TestServer_HTTPRoundTrip_NotificationProducesNoResponse(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (no content) for notification", resp.StatusCode)
	}
	buf := make([]byte, 32)
	n, _ := resp.Body.Read(buf)
	if n > 0 {
		t.Errorf("expected empty body for notification response, got %q", string(buf[:n]))
	}
}

// TestServer_HTTPRoundTrip_ZeroIDIsNotNotification guards the
// previously-broken case: numeric id 0 is a perfectly valid request
// id per spec, and the server MUST respond. Earlier the int-typed
// ID conflated 0 with "missing".
func TestServer_HTTPRoundTrip_ZeroIDIsNotNotification(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"jsonrpc":"2.0","id":0,"method":"tools/list"}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for id=0 request", resp.StatusCode)
	}
	var got struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(got.ID) != "0" {
		t.Errorf("response id = %q, want %q (server must echo numeric 0)", string(got.ID), "0")
	}
}

// TestServer_HTTPRoundTrip_StringIDPreserved verifies string IDs
// round-trip — they're legal per spec and some clients use them for
// trace correlation. Previously the int-typed ID would have decoded
// them as 0.
func TestServer_HTTPRoundTrip_StringIDPreserved(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"jsonrpc":"2.0","id":"abc-123","method":"tools/list"}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	var got struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(got.ID) != `"abc-123"` {
		t.Errorf("response id = %q, want %q (string id must round-trip verbatim)", string(got.ID), `"abc-123"`)
	}
}

func TestServer_HTTPRoundTrip_ToolError(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	s.RegisterTool(
		mcp.ToolDef{Name: "boom", Description: "always errors"},
		func(_ context.Context, _ json.RawMessage) (mcp.CallResult, error) {
			return mcp.CallResult{}, errors.New("kaboom")
		},
	)

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "")
	_, err := client.Call(context.Background(), "boom", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "kaboom") {
		t.Errorf("error = %v, want substring 'kaboom'", err)
	}
}

func TestServer_HTTPRoundTrip_UnknownTool(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "")
	_, err := client.Call(context.Background(), "nope", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error = %v, want substring 'unknown tool'", err)
	}
}

func TestServer_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPut, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_SSE_DeliversNotifications(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Open SSE stream.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", resp.StatusCode)
	}

	// Push notifications repeatedly until the reader sees one — race-
	// safe alternative to sleeping for "subscriber registered". The
	// scanner exits as soon as it picks up any data.
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.Notify("notifications/progress", map[string]any{"percent": 42})
			}
		}
	}()

	got := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				got <- data
				return
			}
		}
	}()

	defer close(stop)
	select {
	case payload := <-got:
		if !strings.Contains(payload, "notifications/progress") {
			t.Errorf("SSE payload = %q, want method", payload)
		}
		if !strings.Contains(payload, "42") {
			t.Errorf("SSE payload = %q, want percent=42", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSE notification not received within 2s")
	}
}

// TestClient_HTTPSubscribeReceivesPushedNotifications exercises the
// full HTTP push-subscription path: Server.Notify → SSE stream →
// httpTransport's SSE listener → Client.SubscribeAny handler.
// Earlier the HTTP transport ignored notifications entirely; the
// stream stayed open server-side but nobody was reading it on the
// client.
func TestClient_HTTPSubscribeReceivesPushedNotifications(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "")
	defer client.Close()

	got := make(chan string, 1)
	cancel := client.SubscribeAny(func(method string, _ json.RawMessage) {
		select {
		case got <- method:
		default:
		}
	})
	defer cancel()

	// Repeatedly notify until the subscriber picks one up — the SSE
	// listener races subscribe registration against the first
	// outbound notification, so the deterministic shape is "keep
	// pushing until received."
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.Notify("notifications/progress", map[string]any{"step": 1})
			}
		}
	}()

	select {
	case method := <-got:
		if method != "notifications/progress" {
			t.Errorf("method = %q, want notifications/progress", method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP subscriber did not receive notification within 5s")
	}
}

// TestHTTPClient_SSEMultiLineDataIsConcatenated guards
// decodeSSEResponse's multi-line accumulation. SSE permits a single
// event to span multiple `data:` lines; the decoder must join them
// with `\n` and parse the result as one JSON-RPC response. Earlier
// the decoder json.Unmarshal'd each line independently and silently
// dropped events whose payload exceeded one line.
//
// The test stands up a stub HTTP server that splits a tools/list
// response across two data lines. If the decoder concatenates, the
// client receives the tool list; otherwise the call hangs / errors.
func TestHTTPClient_SSEMultiLineDataIsConcatenated(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The client speaks JSON-RPC over POST and accepts either
		// application/json or text/event-stream. Force SSE so we
		// exercise the multi-line decoder.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		// Pull the inbound id off the body so we can echo it back.
		var inbound struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&inbound)

		// Per SSE spec, multi-line data: events emit one "data: <chunk>"
		// per line and terminate with a blank line. The decoder
		// concatenates the chunks with "\n" before unmarshalling.
		//
		// We split a single JSON-RPC response across THREE data
		// lines at structural-whitespace boundaries. Joining with
		// "\n" gives valid JSON (JSON tolerates whitespace, including
		// newlines, between tokens). A per-line parse would fail on
		// any of the fragments because none of them is a complete
		// JSON value.
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\n"))
		_, _ = w.Write([]byte("data: \"id\":" + string(inbound.ID) + ",\n"))
		_, _ = w.Write(
			[]byte(
				"data: \"result\":{\"tools\":[{\"name\":\"echo\",\"description\":\"x\",\"inputSchema\":{\"type\":\"object\"}}]}}\n",
			),
		)
		_, _ = w.Write([]byte("\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "")
	defer client.Close()

	// Discover calls tools/list under the hood — the response body
	// is what we constructed above. We don't care about the returned
	// tools beyond "the call returned successfully" — the fact that
	// the decoder didn't choke on the split JSON is the proof.
	tools, err := client.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover through multi-line SSE: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("tools = %+v, want one tool named 'echo' (multi-line concat broken?)", tools)
	}
}

func TestServer_SSE_RequiresEventStreamAccept(t *testing.T) {
	t.Parallel()
	s := mcp.NewServer("test", "1.0")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406", resp.StatusCode)
	}
}

func TestServer_NotifyDropsOnFullSubscriber(t *testing.T) {
	t.Parallel()
	// Notify is non-blocking; fire a burst with no subscribers connected
	// and verify it doesn't deadlock or panic.
	s := mcp.NewServer("test", "1.0")
	for range 100 {
		s.Notify("notifications/test", map[string]any{"i": 1})
	}
}
