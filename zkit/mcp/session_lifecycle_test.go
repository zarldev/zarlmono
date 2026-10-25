package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

type lifecycleRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func decodeLifecycleRequest(t *testing.T, request *http.Request) lifecycleRequest {
	t.Helper()
	var rpc lifecycleRequest
	if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return rpc
}

func writeLifecycleResult(t *testing.T, response http.ResponseWriter, request lifecycleRequest, result any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func writeLifecycleMethodNotFound(t *testing.T, response http.ResponseWriter, request lifecycleRequest) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(map[string]any{
		"jsonrpc": "2.0", "id": request.ID,
		"error": map[string]any{"code": -32601, "message": "method not found"},
	}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestHTTPSessionExpiryFailsMutationThenRenegotiates(t *testing.T) {
	var initializes atomic.Int32
	var toolCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		rpc := decodeLifecycleRequest(t, request)
		session := request.Header.Get("Mcp-Session-Id")
		switch rpc.Method {
		case "server/discover":
			if session != "" {
				t.Errorf("server/discover carried expired session %q", session)
			}
			response.Header().Set("Mcp-Session-Id", "untrusted-discovery-session")
			writeLifecycleMethodNotFound(t, response, rpc)
		case "initialize":
			if session != "" {
				t.Errorf("initialize carried session %q", session)
			}
			generation := initializes.Add(1)
			response.Header().Set("Mcp-Session-Id", fmt.Sprintf("session-%d", generation))
			response.Header().Set("Mcp-Protocol-Version", "2025-11-25")
			writeLifecycleResult(t, response, rpc, map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "session-peer", "version": "1"},
			})
		case "notifications/initialized":
			want := fmt.Sprintf("session-%d", initializes.Load())
			if session != want {
				t.Errorf("initialized session = %q, want %q", session, want)
			}
			response.WriteHeader(http.StatusAccepted)
		case "tools/call":
			toolCalls.Add(1)
			if session != "session-1" {
				t.Errorf("tools/call session = %q, want session-1", session)
			}
			response.WriteHeader(http.StatusNotFound)
		case "tools/list":
			if session != "session-2" {
				t.Errorf("tools/list session = %q, want session-2", session)
			}
			writeLifecycleResult(t, response, rpc, map[string]any{"tools": []any{}})
		default:
			t.Errorf("unexpected method %q", rpc.Method)
			response.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	client := mcp.NewClient(server.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Call(t.Context(), "mutate", map[string]any{"value": 1}); err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("expired Call error = %v", err)
	}
	if got := toolCalls.Load(); got != 1 {
		t.Fatalf("tools/call requests = %d, want 1 (no replay)", got)
	}
	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatalf("Discover after expiry: %v", err)
	}
	if got := initializes.Load(); got != 2 {
		t.Fatalf("initialize requests = %d, want 2", got)
	}
	if got := toolCalls.Load(); got != 1 {
		t.Fatalf("tools/call requests after recovery = %d, want 1", got)
	}
}

func TestHTTPFinalCloseCancelsDrainsDeletesAndRejects(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	deleteSeen := make(chan string, 1)
	var startOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deleteSeen <- request.Header.Get("Mcp-Session-Id")
			response.WriteHeader(http.StatusNoContent)
			return
		}
		rpc := decodeLifecycleRequest(t, request)
		switch rpc.Method {
		case "server/discover":
			writeLifecycleMethodNotFound(t, response, rpc)
		case "initialize":
			response.Header().Set("Mcp-Session-Id", "close-session")
			writeLifecycleResult(t, response, rpc, map[string]any{
				"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
				"serverInfo": map[string]any{"name": "close-peer", "version": "1"},
			})
		case "notifications/initialized":
			response.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeLifecycleResult(t, response, rpc, map[string]any{"tools": []any{}})
		case "tools/call":
			startOnce.Do(func() { close(requestStarted) })
			<-request.Context().Done()
			close(requestCanceled)
		default:
			response.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	client := mcp.NewClient(server.URL, "")
	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatalf("bootstrap Discover: %v", err)
	}
	callDone := make(chan error, 1)
	ctx := t.Context()
	go func() {
		_, err := client.Call(ctx, "blocked", map[string]any{})
		callDone <- err
	}()
	<-requestStarted

	closeResults := make(chan error, 2)
	go func() { closeResults <- client.Close() }()
	go func() { closeResults <- client.Close() }()
	<-requestCanceled
	for range 2 {
		if err := <-closeResults; err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	if err := <-callDone; err == nil || !strings.Contains(err.Error(), "transport closed") {
		t.Fatalf("active Call error = %v", err)
	}
	if session := <-deleteSeen; session != "close-session" {
		t.Fatalf("DELETE session = %q, want close-session", session)
	}
	if _, err := client.Discover(t.Context()); err == nil || !strings.Contains(err.Error(), "transport closed") {
		t.Fatalf("Discover after Close error = %v", err)
	}
}

func TestHTTPCallPreservesCustomCancellationCause(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		rpc := decodeLifecycleRequest(t, request)
		switch rpc.Method {
		case "server/discover":
			writeLifecycleResult(t, response, rpc, map[string]any{
				"resultType": "complete", "supportedVersions": []string{"2026-07-28"},
				"capabilities": map[string]any{"tools": map[string]any{}},
			})
		case "tools/list":
			close(started)
			<-request.Context().Done()
		default:
			response.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	client := mcp.NewClient(server.URL, "")
	t.Cleanup(func() { _ = client.Close() })

	cause := errors.New("caller stopped discovery")
	ctx, cancel := context.WithCancelCause(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := client.Discover(ctx)
		done <- err
	}()
	<-started
	cancel(cause)
	if err := <-done; !errors.Is(err, cause) {
		t.Fatalf("Discover error = %v, want cause %v", err, cause)
	}
}

func TestHTTPNegotiationRejectsInvalidPeerBeforeExecution(t *testing.T) {
	t.Run("initialize response header version", func(t *testing.T) {
		var execution atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			rpc := decodeLifecycleRequest(t, request)
			switch rpc.Method {
			case "server/discover":
				writeLifecycleMethodNotFound(t, response, rpc)
			case "initialize":
				response.Header().Set("Mcp-Protocol-Version", "2024-11-05")
				response.Header().Set("Mcp-Session-Id", "must-not-be-accepted")
				writeLifecycleResult(t, response, rpc, map[string]any{
					"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
					"serverInfo": map[string]any{"name": "bad-header", "version": "1"},
				})
			default:
				execution.Add(1)
				response.WriteHeader(http.StatusBadRequest)
			}
		}))
		t.Cleanup(server.Close)
		client := mcp.NewClient(server.URL, "")
		t.Cleanup(func() { _ = client.Close() })
		if _, err := client.Discover(t.Context()); err == nil || !strings.Contains(err.Error(), "protocol header") {
			t.Fatalf("Discover error = %v", err)
		}
		if got := execution.Load(); got != 0 {
			t.Fatalf("post-initialize executions = %d, want 0", got)
		}
	})

	t.Run("discovery capabilities", func(t *testing.T) {
		var execution atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			rpc := decodeLifecycleRequest(t, request)
			if rpc.Method != "server/discover" {
				execution.Add(1)
			}
			writeLifecycleResult(t, response, rpc, map[string]any{
				"resultType": "complete", "supportedVersions": []string{"2026-07-28"},
				"capabilities": []any{"not", "an", "object"},
			})
		}))
		t.Cleanup(server.Close)
		client := mcp.NewClient(server.URL, "")
		t.Cleanup(func() { _ = client.Close() })
		if _, err := client.Discover(t.Context()); err == nil || !strings.Contains(err.Error(), "capabilities") {
			t.Fatalf("Discover error = %v", err)
		}
		if got := execution.Load(); got != 0 {
			t.Fatalf("executions after malformed capabilities = %d, want 0", got)
		}
	})
}

func TestHTTPModernParametersPreserveInt64AndRejectInvalidBeforeToolCall(t *testing.T) {
	const precise = int64(9_007_199_254_740_991)
	var toolCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		rpc := decodeLifecycleRequest(t, request)
		switch rpc.Method {
		case "server/discover":
			writeLifecycleResult(t, response, rpc, map[string]any{
				"resultType": "complete", "supportedVersions": []string{"2026-07-28"},
				"capabilities": map[string]any{"tools": map[string]any{}},
			})
		case "tools/list":
			writeLifecycleResult(t, response, rpc, map[string]any{
				"resultType": "complete",
				"tools":      []any{map[string]any{"name": "precise", "inputSchema": map[string]any{"type": "object"}}},
			})
		case "tools/call":
			toolCalls.Add(1)
			if !strings.Contains(string(rpc.Params), `"value":9007199254740991`) {
				t.Errorf("tools/call params lost int64 precision: %s", rpc.Params)
			}
			writeLifecycleResult(t, response, rpc, map[string]any{
				"resultType": "complete", "content": []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			response.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	client := mcp.NewClient(server.URL, "")
	t.Cleanup(func() { _ = client.Close() })

	if _, err := client.Call(t.Context(), "precise", map[string]any{"value": precise}); err != nil {
		t.Fatalf("precise Call: %v", err)
	}
	if _, err := client.Call(t.Context(), "precise", map[string]any{"invalid": func() {}}); err == nil {
		t.Fatal("Call accepted non-JSON parameters")
	}
	if got := toolCalls.Load(); got != 1 {
		t.Fatalf("tools/call requests = %d, want 1", got)
	}
}
