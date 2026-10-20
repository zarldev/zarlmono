package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestStdioPreCanceledCallsPreserveClient(t *testing.T) {
	for _, protocol := range []string{"2024-11-05", "2026-07-28"} {
		t.Run(protocol, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			client, err := mcp.NewStdioClientContext(ctx, os.Args[0], []string{"-test.run=^TestStdioCancellationPeer$"}, map[string]string{
				"ZARL_MCP_CANCELLATION_PEER": protocol,
			})
			if err != nil {
				t.Fatalf("construct client: %v", err)
			}
			t.Cleanup(func() { _ = client.Close() })

			canceled, cancelCall := context.WithCancelCause(ctx)
			cause := errors.New("request canceled before write admission")
			cancelCall(cause)
			// Both the ready gate and write gate can race an already-ready Done.
			// Repetition exercises that select choice; each live response is a
			// wire-order barrier proving no canceled request reached the peer.
			for i := range 64 {
				_, err := client.Call(canceled, "count", nil)
				if !errors.Is(err, cause) {
					t.Fatalf("canceled call %d: %v, want original cause", i, err)
				}
				result, err := client.Call(ctx, "count", nil)
				if err != nil {
					t.Fatalf("live call %d after canceled request: %v", i, err)
				}
				if got, want := result.FirstText(), strconv.Itoa(i+1); got != want {
					t.Fatalf("peer call count = %q, want %q; canceled request reached peer", got, want)
				}
			}
		})
	}
}

// TestStdioCancellationPeer runs in a child process and counts every tool call,
// including any request incorrectly admitted after its caller was canceled.
func TestStdioCancellationPeer(t *testing.T) {
	protocol := os.Getenv("ZARL_MCP_CANCELLATION_PEER")
	if protocol == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	calls := 0
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			t.Fatalf("decode peer request: %v", err)
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "server/discover":
			if protocol == "2026-07-28" {
				response["result"] = map[string]any{
					"resultType": "complete", "supportedVersions": []string{protocol}, "capabilities": map[string]any{},
				}
			} else {
				response["error"] = map[string]any{"code": -32601, "message": "method not found"}
			}
		case "initialize":
			response["result"] = map[string]any{
				"protocolVersion": protocol, "capabilities": map[string]any{},
				"serverInfo": map[string]any{"name": "cancellation-peer", "version": "1"},
			}
		case "notifications/initialized", "notifications/cancelled":
			continue
		case "tools/call":
			calls++
			response["result"] = map[string]any{
				"resultType": "complete",
				"content":    []map[string]any{{"type": "text", "text": strconv.Itoa(calls)}},
			}
		case "requests/cancel":
			response["result"] = map[string]any{"resultType": "complete"}
		default:
			t.Fatalf("unexpected peer method %q", request.Method)
		}
		if err := encoder.Encode(response); err != nil {
			return // The parent may close the transport after a failing assertion.
		}
	}
}
