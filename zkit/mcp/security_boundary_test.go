package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestHTTPClientRejectsAllEndpointRedirects(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var leaked atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
			t.Cleanup(target.Close)
			approved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, status)
			}))
			t.Cleanup(approved.Close)
			client := mcp.NewClient(approved.URL, "synthetic-bearer-canary")
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.Discover(t.Context()); err == nil || !strings.Contains(err.Error(), "redirects are not allowed") {
				t.Fatalf("redirect was not rejected: %v", err)
			}
			if leaked.Load() != 0 {
				t.Fatal("unapproved endpoint received credentials or a replayed request")
			}
		})
	}
}

func TestServerRejectsUnapprovedBrowserOriginsBeforeDispatch(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, origins := range [][]string{{"http://rebinding.example"}, {"null"}, {""}, {"https://allowed.example", "http://evil.example"}, {"https://allowed.example/path"}} {
			t.Run(method+"/"+strings.Join(origins, ","), func(t *testing.T) {
				t.Parallel()
				server := mcp.NewServer("fixture", "1", mcp.WithAllowedOrigins("https://allowed.example"))
				request := httptest.NewRequest(method, "http://rebinding.example/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
				request.Header["Origin"] = origins
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusForbidden {
					t.Fatalf("invalid Origin reached dispatch: status=%d", response.Code)
				}
			})
		}
	}
	for _, origin := range []string{"", "https://allowed.example"} {
		server := mcp.NewServer("fixture", "1", mcp.WithAllowedOrigins("https://allowed.example"))
		request := httptest.NewRequest(http.MethodPost, "http://native.example/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("native/explicitly allowed client rejected: status=%d", response.Code)
		}
	}
}

func TestServerNotifyAndDisconnectShareChannelOwnership(t *testing.T) {
	server := mcp.NewServer("fixture", "1")
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	stop := make(chan struct{})
	var producers sync.WaitGroup
	producers.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				server.Notify("notifications/tools/list_changed", nil)
			}
		}
	})
	defer func() { close(stop); producers.Wait() }()
	for range 100 {
		ctx, cancel := context.WithCancel(t.Context())
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		request.Header.Set("Accept", "text/event-stream")
		response, err := httpServer.Client().Do(request)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		_ = response.Body.Close()
		cancel()
	}
}
