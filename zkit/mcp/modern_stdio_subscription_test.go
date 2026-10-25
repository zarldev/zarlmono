package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

const modernStdioPeerEnv = "ZARL_MCP_MODERN_STDIO_PEER"

func TestModernStdioSubscriptionCorrelatesAndCancels(t *testing.T) {
	marker := t.TempDir() + "/cancelled"
	client, err := mcp.NewStdioClient(os.Args[0], []string{"-test.run=^TestModernStdioSyntheticPeer$"}, map[string]string{
		modernStdioPeerEnv:             "1",
		"ZARL_MCP_MODERN_STDIO_MARKER": marker,
	})
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = client.Close()
		}
	})

	notifications := make(chan json.RawMessage, 3)
	cancel := client.Subscribe("notifications/tools/list_changed", func(params json.RawMessage) {
		notifications <- params
	})
	defer cancel()

	select {
	case params := <-notifications:
		var members map[string]json.RawMessage
		if err := json.Unmarshal(params, &members); err != nil {
			t.Fatalf("decode notification params: %v", err)
		}
		if got := string(members["count"]); got != "9007199254740993" {
			t.Fatalf("count = %s, want exact large integer", got)
		}
		var metadata map[string]json.RawMessage
		if err := json.Unmarshal(members["_meta"], &metadata); err != nil {
			t.Fatalf("decode notification metadata: %v", err)
		}
		if got := string(metadata["peer"]); got != `"kept"` {
			t.Fatalf("peer metadata = %s, want preserved value", got)
		}
		if got := string(metadata["io.modelcontextprotocol/subscriptionId"]); got != `"subscription"` {
			t.Fatalf("subscription id = %s, want originating request id", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("modern stdio subscription was not opened")
	}
	select {
	case extra := <-notifications:
		t.Fatalf("uncorrelated notification was delivered: %s", extra)
	default:
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true
	if data, err := os.ReadFile(marker); err != nil || string(data) != "requests/cancel\n" {
		t.Fatalf("modern cancellation marker = %q, %v", data, err)
	}
}

func TestModernHTTPSubscriptionAddsCorrelationWithoutMutatingParams(t *testing.T) {
	server := mcp.NewServer("test", "1")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	body := `{"jsonrpc":"2.0","id":9007199254740993,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	request.Header.Set("Mcp-Method", "subscriptions/listen")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open subscription: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("subscription status = %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	ack := nextSSEPayload(t, scanner)
	assertSubscriptionID(t, ack, "9007199254740993")

	metadata := map[string]any{"peer": "kept"}
	params := map[string]any{"count": int64(9007199254740993), "_meta": metadata}
	server.Notify("notifications/tools/list_changed", params)
	note := nextSSEPayload(t, scanner)
	assertSubscriptionID(t, note, "9007199254740993")
	var envelope struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(note, &envelope); err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	if got := string(envelope.Params["count"]); got != "9007199254740993" {
		t.Fatalf("count = %s, want exact large integer", got)
	}
	var gotMetadata map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Params["_meta"], &gotMetadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if got := string(gotMetadata["peer"]); got != `"kept"` {
		t.Fatalf("peer metadata = %s, want preserved value", got)
	}
	if _, mutated := metadata["io.modelcontextprotocol/subscriptionId"]; mutated {
		t.Fatal("Server.Notify mutated caller metadata")
	}
}

func nextSSEPayload(t *testing.T, scanner *bufio.Scanner) json.RawMessage {
	t.Helper()
	for scanner.Scan() {
		if payload, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			return json.RawMessage(payload)
		}
	}
	t.Fatalf("subscription stream ended: %v", scanner.Err())
	return nil
}

func assertSubscriptionID(t *testing.T, payload json.RawMessage, want string) {
	t.Helper()
	var envelope struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode subscription event: %v", err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Params["_meta"], &metadata); err != nil {
		t.Fatalf("decode subscription event metadata: %v", err)
	}
	if got := string(metadata["io.modelcontextprotocol/subscriptionId"]); got != want {
		t.Fatalf("subscription id = %s, want %s", got, want)
	}
}

// TestModernStdioSyntheticPeer is executed in a child test process. It is a
// strict protocol peer: malformed discovery, subscription, or cancellation
// requests terminate the stream instead of supplying a permissive fallback.
func TestModernStdioSyntheticPeer(t *testing.T) {
	if os.Getenv(modernStdioPeerEnv) != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil || request.JSONRPC != "2.0" || len(request.ID) == 0 {
			return
		}
		switch request.Method {
		case "server/discover":
			if !hasModernProtocolMetadata(request.Params) {
				return
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": request.ID,
				"result": map[string]any{
					"resultType": "complete", "supportedVersions": []string{"2026-07-28"},
					"capabilities": map[string]any{"tools": map[string]any{}},
				},
			})
		case "subscriptions/listen":
			if string(request.ID) != `"subscription"` || !hasModernProtocolMetadata(request.Params) || !strings.Contains(string(request.Params), `"notifications"`) {
				return
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged", "params": map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/subscriptionId": "subscription"}}})
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed", "params": map[string]any{"bad": "wrong id", "_meta": map[string]any{"io.modelcontextprotocol/subscriptionId": "other"}}})
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed", "params": map[string]any{"bad": "missing metadata"}})
			_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{"count":9007199254740993,"_meta":{"peer":"kept","io.modelcontextprotocol/subscriptionId":"subscription"}}}`)
		case "requests/cancel":
			var params map[string]json.RawMessage
			if json.Unmarshal(request.Params, &params) != nil || string(params["requestId"]) != `"subscription"` || !hasModernProtocolMetadata(request.Params) {
				return
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"resultType": "complete"}})
			_ = os.WriteFile(os.Getenv("ZARL_MCP_MODERN_STDIO_MARKER"), []byte("requests/cancel\n"), 0o600)
		default:
			return
		}
	}
}

func hasModernProtocolMetadata(params json.RawMessage) bool {
	var members map[string]json.RawMessage
	if json.Unmarshal(params, &members) != nil {
		return false
	}
	var metadata map[string]json.RawMessage
	return json.Unmarshal(members["_meta"], &metadata) == nil && string(metadata["io.modelcontextprotocol/protocolVersion"]) == `"2026-07-28"`
}
