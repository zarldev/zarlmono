package mcp

import (
	"net"
	"net/netip"
	"strings"
	"testing"
)

// TestValidatingDialContext_RejectsByPolicy guards the per-dial
// validation that closes the DNS-rebinding window the adversarial
// review flagged (docs/adversarial-repo-review.md #4). The
// dial-time check has to fire on the literal-IP path AND the
// hostname-resolution path; both run through the same policy.
//
// Literal-IP case is the cleanest assertion: we pass a numeric
// address and a policy that rejects every IP, and the dial must
// fail with an error mentioning "rejected by policy" — proving
// the validator ran BEFORE any socket open.
func TestValidatingDialContext_RejectsByPolicy(t *testing.T) {
	t.Parallel()
	denyAll := AddrPolicy(func(netip.Addr) bool { return false })
	dial := validatingDialContext(denyAll)

	_, err := dial(t.Context(), "tcp", "127.0.0.1:9")
	if err == nil {
		t.Fatalf("expected dial rejection; got nil")
	}
	if !strings.Contains(err.Error(), "rejected by policy") {
		t.Fatalf("expected policy-rejection error; got %q", err.Error())
	}
}

// TestValidatingDialContext_AllowsApprovedLiteralIP exercises the
// happy path: a policy that allows the dialed IP, against a real
// in-process listener. Proves the validator doesn't introduce a
// regression for the allowed case.
func TestValidatingDialContext_AllowsApprovedLiteralIP(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	allowLoopback := AddrPolicy(func(ip netip.Addr) bool { return ip.IsLoopback() })
	dial := validatingDialContext(allowLoopback)

	conn, err := dial(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial against allowed listener: %v", err)
	}
	defer conn.Close()
	if conn.RemoteAddr().String() != ln.Addr().String() {
		t.Fatalf("dial landed on %q; want %q", conn.RemoteAddr().String(), ln.Addr().String())
	}
}

// TestValidatingDialContext_RejectsResolvedHostname covers the
// hostname path: even when the address parses as a host (not a
// literal IP), the resolved answer must be policy-checked. We
// use a hostname that resolves to loopback ("localhost") with a
// policy that rejects loopback — the dial must fail rather than
// silently connecting because the literal-IP fast path didn't
// fire.
func TestValidatingDialContext_RejectsResolvedHostname(t *testing.T) {
	t.Parallel()
	rejectLoopback := AddrPolicy(func(ip netip.Addr) bool { return !ip.IsLoopback() })
	dial := validatingDialContext(rejectLoopback)

	_, err := dial(t.Context(), "tcp", "localhost:9")
	if err == nil {
		t.Fatalf("expected loopback rejection via hostname resolution; got nil")
	}
	// The hostname path's error wording either flags every
	// resolved IP as rejected, or — if the resolver returns no
	// addresses we can use — says so. Either form proves the
	// guard fired before opening a socket; the negative case
	// (silent connect to 127.0.0.1:9 then connection refused
	// from the kernel) would surface "connection refused" or
	// similar, which is what we're guarding against.
	if strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("policy bypassed: dial reached the socket and got refused (%v)", err)
	}
}
