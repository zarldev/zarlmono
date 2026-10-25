package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestLoopbackClientRejectsRedirects(t *testing.T) {
	for _, target := range []string{"other-port", "same-origin-path", "public", "metadata"} {
		t.Run(target, func(t *testing.T) {
			var redirected atomic.Int64
			other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				redirected.Add(1)
			}))
			t.Cleanup(other.Close)
			location := other.URL
			switch target {
			case "same-origin-path":
				location = "/other"
			case "public":
				location = "http://192.0.2.1/mcp"
			case "metadata":
				location = "http://169.254.169.254/"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/other" {
					redirected.Add(1)
					return
				}
				http.Redirect(w, r, location, http.StatusTemporaryRedirect)
			}))
			t.Cleanup(server.Close)
			client := mcp.NewLoopbackClient(server.URL, "")
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.Discover(t.Context()); err == nil || !strings.Contains(err.Error(), "redirects are not allowed") {
				t.Fatalf("Discover error = %v, want redirect rejection", err)
			}
			if got := redirected.Load(); got != 0 {
				t.Fatalf("unapproved endpoint received %d requests", got)
			}
		})
	}
}

func TestLoopbackClientRejectsNonLoopbackDial(t *testing.T) {
	for _, endpoint := range []string{"http://192.0.2.1/", "http://10.0.0.1/", "http://169.254.169.254/"} {
		t.Run(endpoint, func(t *testing.T) {
			client := mcp.NewLoopbackClient(endpoint, "")
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.Discover(t.Context()); err == nil || !strings.Contains(err.Error(), "rejected by policy") {
				t.Fatalf("Discover error = %v, want dial rejection", err)
			}
		})
	}
}

func TestLoopbackClientIgnoresProxyEnvironment(t *testing.T) {
	var proxied atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		proxied.Add(1)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	server := httptest.NewServer(mcp.NewServer("local", "1").Handler())
	t.Cleanup(server.Close)
	client := mcp.NewLoopbackClient(server.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatalf("Discover local endpoint: %v", err)
	}
	if got := proxied.Load(); got != 0 {
		t.Fatalf("proxy received %d requests", got)
	}
}
