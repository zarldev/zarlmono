package fetch

import (
	"net/netip"
	"testing"
)

func TestDisallowedAddr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},        // loopback
		{"::1", true},              // loopback v6
		{"::ffff:127.0.0.1", true}, // v4-mapped loopback
		{"169.254.169.254", true},  // link-local (cloud metadata)
		{"10.0.0.5", true},         // private
		{"192.168.1.1", true},      // private
		{"172.16.0.1", true},       // private
		{"0.0.0.0", true},          // unspecified
		{"224.0.0.1", true},        // multicast
		{"8.8.8.8", false},         // public
		{"1.1.1.1", false},         // public
		{"93.184.216.34", false},   // public (example.com)
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			addr := netip.MustParseAddr(tt.addr)
			if got := disallowedAddr(addr); got != tt.want {
				t.Fatalf("disallowedAddr(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestGuardURLHostRejectsInternal(t *testing.T) {
	t.Parallel()
	// IP-literal and localhost cases resolve without DNS, so they're
	// deterministic offline.
	blocked := []string{
		"http://localhost/",
		"http://localhost:8080/admin",
		"http://foo.localhost/",
		"http://127.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://10.1.2.3/",
		"http://192.168.0.1/",
		"http://0.0.0.0/",
	}
	for _, raw := range blocked {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if err := guardURLHost(t.Context(), raw); err == nil {
				t.Fatalf("guardURLHost(%q) = nil, want rejection", raw)
			}
		})
	}
}

func TestGuardURLHostAllowsPublicLiteral(t *testing.T) {
	t.Parallel()
	// Public IP literals skip DNS, so this is offline-safe.
	for _, raw := range []string{"http://8.8.8.8/", "https://1.1.1.1/"} {
		if err := guardURLHost(t.Context(), raw); err != nil {
			t.Fatalf("guardURLHost(%q) = %v, want allow", raw, err)
		}
	}
}
