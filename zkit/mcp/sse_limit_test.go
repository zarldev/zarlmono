package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestSSEAggregateLimit(t *testing.T) {
	t.Parallel()
	for _, notifications := range []bool{false, true} {
		name := "response"
		if notifications {
			name = "notifications"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			disconnected := make(chan struct{})
			var once sync.Once
			var requests atomic.Int32
			reconnected := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) > 1 {
					select {
					case reconnected <- struct{}{}:
					default:
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				// Every line is below the scanner limit; the event is not.
				line := "data: " + strings.Repeat(" ", 64*1024) + "\n"
				for range 65 {
					if _, err := io.WriteString(w, line); err != nil {
						once.Do(func() { close(disconnected) })
						return
					}
				}
				if err := http.NewResponseController(w).Flush(); err != nil {
					once.Do(func() { close(disconnected) })
					return
				}
				// No event terminator or EOF: the size bound must stop the read.
				<-r.Context().Done()
				once.Do(func() { close(disconnected) })
			}))
			t.Cleanup(server.Close)
			client := mcp.NewClient(server.URL, "")
			t.Cleanup(func() { _ = client.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if notifications {
				unsubscribe := client.SubscribeAny(func(string, json.RawMessage) {
					t.Error("oversized event was dispatched")
				})
				defer unsubscribe()
			} else {
				_, err := client.Discover(ctx)
				if !errors.Is(err, mcp.ErrSSEEventTooLarge) {
					t.Fatalf("Discover error = %v, want ErrSSEEventTooLarge", err)
				}
			}
			select {
			case <-disconnected:
			case <-ctx.Done():
				t.Fatal("oversized stream was not closed")
			}
			if notifications {
				select {
				case <-reconnected:
					t.Fatal("reconnected after terminal size violation")
				case <-time.After(1500 * time.Millisecond):
				}
			}
		})
	}
}

func TestSSEMultilineWithinLimit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\n"+
			"data: \"id\":1,\"result\":{\"tools\":[]}}\n\n")
	}))
	t.Cleanup(server.Close)
	client := mcp.NewClient(server.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSSESingleLineLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		extra int
	}{
		{"exact limit", 0}, {"over event limit", 1}, {"over scanner limit", 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload := `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
			payload += strings.Repeat(" ", 4*1024*1024+tc.extra-len(payload))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+payload+"\r\n\r\n")
			}))
			t.Cleanup(server.Close)
			client := mcp.NewClient(server.URL, "")
			t.Cleanup(func() { _ = client.Close() })
			_, err := client.Discover(t.Context())
			if tc.extra == 0 && err != nil {
				t.Fatal(err)
			}
			if tc.extra > 0 && !errors.Is(err, mcp.ErrSSEEventTooLarge) {
				t.Fatalf("error = %v, want ErrSSEEventTooLarge", err)
			}
		})
	}
}
