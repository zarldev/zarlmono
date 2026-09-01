package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestSubscriptionsRouteHTTPNotifications(t *testing.T) {
	t.Parallel()

	connected := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response does not support flushing")
			return
		}
		close(connected)
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"foo\",\"params\":{\"task_id\":42}}\n\n"))
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"bar\",\"params\":{}}\n\n"))
		flusher.Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)

	client := mcp.NewClient(srv.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	var specific, catchAll atomic.Int32
	payloads := make(chan json.RawMessage, 1)
	cancelSpecific := client.Subscribe("foo", func(payload json.RawMessage) {
		specific.Add(1)
		payloads <- payload
	})
	cancelAny := client.SubscribeAny(func(string, json.RawMessage) { catchAll.Add(1) })

	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("notification stream did not connect")
	}
	select {
	case payload := <-payloads:
		if string(payload) != `{"task_id":42}` {
			t.Fatalf("payload = %s, want verbatim task payload", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("specific subscription did not fire")
	}
	for catchAll.Load() != 2 {
		select {
		case <-time.After(3 * time.Second):
			t.Fatalf("catch-all hits = %d, want 2", catchAll.Load())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if specific.Load() != 1 {
		t.Fatalf("specific hits = %d, want 1", specific.Load())
	}

	cancelSpecific()
	cancelAny()
	close(release)
}
