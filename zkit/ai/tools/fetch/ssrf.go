package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// web_fetch takes a fully model-controlled URL, so without these guards a
// hostile model (or prompt-injected page content steering it) can reach the
// cloud metadata endpoint, localhost admin ports, or internal RFC1918
// services — classic SSRF. The defense mirrors the MCP HTTP connect path
// (see dynamic/mcp_connect.go): reject the host up front AND re-check the
// actual dialed IP at connect time so DNS rebinding and redirect-to-internal
// can't slip past a name-only check.

// disallowedAddr reports whether an IP must never be dialed by web_fetch.
// Kept in sync with dynamic.isDisallowedMCPAddr (that one is unexported, so
// the predicate is duplicated rather than shared — consolidate if a third
// caller appears).
func disallowedAddr(addr netip.Addr) bool {
	addr = addr.Unmap() // normalise ::ffff:127.0.0.1 → 127.0.0.1
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified()
}

// guardedTransport returns the default zhttp transport with a dial-time IP
// gate. The Control hook runs after DNS resolution with the concrete address
// about to be dialed, so it closes the DNS-rebinding window and re-validates
// every redirect hop (each hop opens a fresh dial through this transport).
func guardedTransport() http.RoundTripper {
	tr := zhttp.DefaultTransport()
	tr.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("dial %q: %w", address, err)
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("dial %q: unparseable address", address)
			}
			if disallowedAddr(addr) {
				return fmt.Errorf("dial blocked: %q is a disallowed (internal/loopback) address", host)
			}
			return nil
		},
	}).DialContext
	return tr
}

// guardURLHost is the pre-flight host check, giving a fast, clear error
// before any connection and protecting the browser path (which does its own
// DNS and is otherwise unreachable by the dial gate). It rejects localhost,
// IP-literal internal targets, and any hostname that resolves to a
// disallowed address.
func guardURLHost(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("url host required")
	}
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("host %q is local and not allowed", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if disallowedAddr(addr) {
			return fmt.Errorf("host %q is a disallowed (internal/loopback) address", host)
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("resolve host %q: no addresses", host)
	}
	for _, ip := range addrs {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			return fmt.Errorf("resolve host %q: invalid address", host)
		}
		if disallowedAddr(addr) {
			return fmt.Errorf("host %q resolves to disallowed address %q", host, addr.Unmap())
		}
	}
	return nil
}
