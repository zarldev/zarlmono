package mcp_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestClientDialPolicyRejectsLiteralIP(t *testing.T) {
	t.Parallel()

	client := mcp.NewClientWithDialPolicy("http://127.0.0.1:9", "", func(netip.Addr) bool { return false })
	_, err := client.Discover(t.Context())
	if err == nil || !strings.Contains(err.Error(), "rejected by policy") {
		t.Fatalf("Discover error = %v, want policy rejection", err)
	}
}

func TestClientDialPolicyAllowsApprovedLiteralIP(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	t.Cleanup(srv.Close)

	client := mcp.NewClientWithDialPolicy(srv.URL, "", func(ip netip.Addr) bool { return ip.IsLoopback() })
	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatalf("Discover through approved address: %v", err)
	}
}

func TestClientDialPolicyRejectsResolvedHostname(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	srv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	url := "http://localhost:" + strings.Split(ln.Addr().String(), ":")[1]
	client := mcp.NewClientWithDialPolicy(url, "", func(ip netip.Addr) bool { return !ip.IsLoopback() })
	_, err = client.Discover(t.Context())
	if err == nil || strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("Discover error = %v, want hostname policy rejection before connection", err)
	}
}
