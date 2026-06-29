package dynamic_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

// TestDefaultMCPConnectPolicy_RejectsBearerOverHTTP guards the
// "cleartext bearer token" finding (docs/adversarial-repo-review.md
// #3): pkg/mcp/http.go attaches the auth_token as
// `Authorization: Bearer <token>` on every request. Over http://
// that exposes the credential to the wire and to every on-path
// proxy. The policy must refuse that combination at connect time —
// before the http client ever opens a socket — and surface a
// clear error pointing the user at https.
//
// Token-less http:// is intentionally still allowed (public /
// unauthenticated MCP servers).
func TestDefaultMCPConnectPolicy_RejectsBearerOverHTTP(t *testing.T) {
	t.Parallel()
	// Use a public IP so validateMCPHTTPBaseURL's loopback /
	// private-net checks don't reject before our scheme check
	// runs — those have their own dedicated tests.
	const publicIP = "203.0.113.10"
	tests := []struct {
		name      string
		baseURL   string
		authToken string
		wantErr   bool
		wantSub   string // expected substring in error (empty when wantErr=false)
	}{
		{
			name:      "https with token allowed",
			baseURL:   "https://" + publicIP + "/mcp",
			authToken: "sk-secret",
			wantErr:   false,
		},
		{
			name:      "http no token allowed",
			baseURL:   "http://" + publicIP + "/mcp",
			authToken: "",
			wantErr:   false,
		},
		{
			name:      "http with token rejected",
			baseURL:   "http://" + publicIP + "/mcp",
			authToken: "sk-secret",
			wantErr:   true,
			wantSub:   "cleartext http",
		},
		{
			name:      "uppercase HTTP scheme still rejected",
			baseURL:   "HTTP://" + publicIP + "/mcp",
			authToken: "sk-secret",
			wantErr:   true,
			wantSub:   "cleartext http",
		},
		{
			name:      "https with no token allowed",
			baseURL:   "https://" + publicIP + "/mcp",
			authToken: "",
			wantErr:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := dynamic.DefaultMCPConnectPolicy.ValidateMCPConnect(
				t.Context(), "test", dynamic.MCPConnSpec{
					Type:      dynamic.Transports.TRANSPORTHTTP,
					BaseURL:   tt.baseURL,
					AuthToken: tt.authToken,
				})
			if (err != nil) != tt.wantErr {
				t.Fatalf("policy(%q, token=%q) err = %v; wantErr=%v",
					tt.baseURL, tt.authToken, err, tt.wantErr)
			}
			if tt.wantSub != "" && err != nil && !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("policy(%q) err = %q; want substring %q",
					tt.baseURL, err.Error(), tt.wantSub)
			}
		})
	}
}

func TestValidateMCPHTTPBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "public https ip", raw: "https://93.184.216.34/mcp", wantErr: false},
		{name: "public http", raw: "http://203.0.113.10/mcp", wantErr: false},
		{name: "bad scheme", raw: "file:///tmp/mcp.sock", wantErr: true},
		{name: "missing host", raw: "https:///mcp", wantErr: true},
		{name: "localhost", raw: "http://localhost:8080", wantErr: true},
		{name: "localhost suffix", raw: "http://foo.localhost:8080", wantErr: true},
		{name: "loopback ipv4", raw: "http://127.0.0.1:8080", wantErr: true},
		{name: "loopback ipv6", raw: "http://[::1]:8080", wantErr: true},
		{name: "private 10", raw: "http://10.0.0.1", wantErr: true},
		{name: "private 172", raw: "http://172.16.0.1", wantErr: true},
		{name: "private 192", raw: "http://192.168.1.5", wantErr: true},
		{name: "metadata", raw: "http://169.254.169.254", wantErr: true},
		{name: "unspecified", raw: "http://0.0.0.0:9999", wantErr: true},
		{name: "multicast", raw: "http://224.0.0.1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := dynamic.DefaultMCPConnectPolicy.ValidateMCPConnect(
				t.Context(), "test", dynamic.MCPConnSpec{
					Type:    dynamic.Transports.TRANSPORTHTTP,
					BaseURL: tt.raw,
				})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateMCPConnect(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
		})
	}
}
