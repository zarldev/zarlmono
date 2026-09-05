package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestHTTPNotificationsDoNotStartAfterClose(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client := mcp.NewClient(server.URL, "")
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { _ = client.SubscribeAny(func(string, json.RawMessage) {}) })
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	if got := requests.Load(); got != 0 {
		t.Fatalf("listener requests after Close = %d, want 0", got)
	}
}

func TestHTTPResponseRejectsTrailingOrOversizeBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "trailing JSON", body: `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}} {}`},
		{name: "over limit", body: `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}` + strings.Repeat(" ", 4*1024*1024)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			client := mcp.NewClient(server.URL, "")
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.Discover(t.Context()); err == nil {
				t.Fatal("Discover succeeded with an invalid complete response body")
			}
		})
	}
}
