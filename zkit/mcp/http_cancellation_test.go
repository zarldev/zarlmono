package mcp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestHTTPResponseBodyPreservesCancellationCause(t *testing.T) {
	for _, tc := range []struct {
		name, method, contentType, prefix string
		status                            int
	}{
		{"json", "tools/list", "application/json", `{"jsonrpc":`, http.StatusOK},
		{"sse", "tools/list", "text/event-stream", ": waiting\n\n", http.StatusOK},
		{"notification", "notifications/initialized", "application/json", "", http.StatusAccepted},
		{"http-error", "tools/list", "application/json", `{"error":`, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flushed, observed, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var gated atomic.Bool
			var observeOnce, releaseOnce sync.Once
			releaseBody := func() { releaseOnce.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rpc := decodeLifecycleRequest(t, r)
				_, _ = io.Copy(io.Discard, r.Body)
				if rpc.Method == tc.method && gated.CompareAndSwap(false, true) {
					w.Header().Set("Content-Type", tc.contentType)
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.prefix)
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Errorf("flush response headers: %v", err)
					}
					close(flushed)
					select {
					case <-release:
					case <-r.Context().Done():
					}
					return
				}
				switch rpc.Method {
				case "server/discover":
					if tc.method == "notifications/initialized" {
						writeLifecycleMethodNotFound(t, w, rpc)
					} else {
						writeLifecycleResult(t, w, rpc, map[string]any{
							"resultType": "complete", "supportedVersions": []string{"2026-07-28"}, "capabilities": map[string]any{},
						})
					}
				case "initialize":
					writeLifecycleResult(t, w, rpc, map[string]any{
						"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
						"serverInfo": map[string]any{"name": "body-peer", "version": "1"},
					})
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
				case "tools/list":
					writeLifecycleResult(t, w, rpc, map[string]any{"resultType": "complete", "tools": []any{}})
				default:
					t.Errorf("unexpected method %q", rpc.Method)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(releaseBody)
			client := mcp.NewLoopbackClient(server.URL, "")
			t.Cleanup(func() { _ = client.Close() })

			bounded, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancelCause(bounded)
			cause := errors.New("caller stopped response body")
			ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
				GotFirstResponseByte: func() {
					if gated.Load() {
						observeOnce.Do(func() { close(observed) })
					}
				},
			})
			done := make(chan struct{})
			var callErr error
			go func() {
				defer close(done)
				_, callErr = client.Discover(ctx)
			}()
			defer func() { cancel(cause); releaseBody(); <-done }()
			for _, signal := range []<-chan struct{}{flushed, observed} {
				select {
				case <-signal:
				case <-done:
					t.Fatalf("Discover returned before body cancellation: %v", callErr)
				case <-bounded.Done():
					t.Fatal("response did not reach the body gate")
				}
			}
			cancel(cause)
			select {
			case <-done:
			case <-bounded.Done():
				t.Fatal("Discover did not stop after body cancellation")
			}
			if !errors.Is(callErr, cause) {
				t.Fatalf("Discover error = %v, want original cause %v", callErr, cause)
			}
			releaseBody()
			if _, err := client.Discover(bounded); err != nil {
				t.Fatalf("Discover after canceled response: %v", err)
			}
		})
	}
}
