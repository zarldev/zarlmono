package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// AddrPolicy decides whether a resolved IP is allowed for an
// outbound HTTP dial. Returns true when ip may be connected to.
//
// Plugged into the transport's DialContext so every dial — first
// connect AND post-Connection-close reconnects — re-validates the
// hostname's current resolution against the policy. Without this,
// a hostname that passed a one-shot validateMCPHTTPBaseURL probe
// at config time can rebind to loopback / RFC1918 / link-local
// during the actual request, turning the MCP HTTP transport into
// an SSRF surface against local/private services.
//
// nil policy is the "don't filter" sentinel — used by the original
// NewClient that predates this guard.
type AddrPolicy func(ip netip.Addr) bool

// httpTransport speaks JSON-RPC 2.0 to a streamable-HTTP MCP server.
// Each call is a single POST; responses may come back as
// `application/json` or as an SSE stream (DeepWiki and friends do the
// latter even for unary calls).
//
// Push notifications: when a consumer calls Subscribe / SubscribeAny
// on the wrapping Client, the transport opens a long-lived GET
// against baseURL with `Accept: text/event-stream` and feeds every
// inbound event to the registered handler. Earlier shape ignored
// pushes entirely on HTTP — the SSE endpoint was running server-side
// but nobody on the client was reading it. Lazy start (only on first
// OnNotification call) keeps the transport free of background
// goroutines when nobody's subscribing.
type httpTransport struct {
	baseURL   string
	authToken string
	http      *http.Client

	// Push-notification plumbing. notifMu serialises (notifier,
	// sseCancel, sseDone) — registering a handler, replacing it, and
	// teardown via Close all touch these together. The SSE goroutine
	// is started at most once per transport lifetime.
	notifMu   sync.Mutex
	notifier  func(method string, params json.RawMessage)
	sseStart  sync.Once // lazy start gate
	sseCtx    context.Context
	sseCancel context.CancelFunc
	sseDone   chan struct{}
}

func newHTTPTransport(baseURL, authToken string, policy AddrPolicy) *httpTransport {
	tr := zhttp.DefaultTransport()
	if policy != nil {
		tr.DialContext = validatingDialContext(policy)
	}
	return &httpTransport{
		baseURL:   baseURL,
		authToken: authToken,
		// SSE wants no total-request timeout — the stream is meant to
		// stay open. We rely on per-call timeouts in Do() instead by
		// using a context with deadline on POST requests, and use zhttp's
		// hardened transport for Dial / TLS / Header / Idle timeouts.
		http: &http.Client{Transport: tr},
	}
}

// validatingDialContext returns a net.Conn dialer that resolves
// the address ITSELF (not letting the default dialer do an
// unchecked lookup) and rejects every resolved IP that policy
// disallows. The DialContext is called by http.Transport on every
// new connection — first connect, reconnect after idle pool
// expiry, redirects — so the rebinding window is closed at the
// only layer below which the TCP socket actually opens.
//
// TLS SNI / Host header semantics survive: http.Transport derives
// ServerName from the request URL's hostname (not from the dialed
// address), so passing a numeric IP into the dial doesn't affect
// the TLS handshake's server-name negotiation.
func validatingDialContext(policy AddrPolicy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("mcp dial: parse %q: %w", addr, err)
		}
		// If the addr is already a literal IP, validate directly —
		// no DNS lookup needed.
		if ip, perr := netip.ParseAddr(host); perr == nil {
			if !policy(ip) {
				return nil, fmt.Errorf("mcp dial: address %q rejected by policy", ip.String())
			}
			d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
			return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		// Hostname path: resolve, filter, dial the first allowed
		// answer. We do the resolution ourselves rather than
		// letting net.Dialer do it because we need the post-
		// filter IP list, not just yes/no on the first DNS reply.
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("mcp dial: resolve %q: %w", host, err)
		}
		var lastErr error
		for _, raw := range ips {
			ip, ok := netip.AddrFromSlice(raw.IP)
			if !ok {
				continue
			}
			if !policy(ip) {
				lastErr = fmt.Errorf("mcp dial: resolved %q to %q which is rejected by policy", host, ip.String())
				continue
			}
			d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
			conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if derr == nil {
				return conn, nil
			}
			lastErr = derr
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("mcp dial: no resolved addresses for %q", host)
		}
		return nil, lastErr
	}
}

// Close tears down any active SSE listener and waits for the
// goroutine to exit so the transport doesn't outlive its consumer.
func (t *httpTransport) Close() error {
	t.notifMu.Lock()
	cancel := t.sseCancel
	done := t.sseDone
	t.notifMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

// OnNotification implements [NotificationReceiver]. The first call
// kicks off the SSE listener goroutine; subsequent calls just swap
// the handler. Pass nil to clear the handler (notifications still
// arrive on the wire but are dropped).
func (t *httpTransport) OnNotification(handler func(method string, params json.RawMessage)) {
	t.notifMu.Lock()
	t.notifier = handler
	t.notifMu.Unlock()
	t.sseStart.Do(t.startSSEListener)
}

// startSSEListener opens a long-lived GET against baseURL with
// Accept: text/event-stream and feeds every inbound JSON-RPC
// notification to the current notifier. Runs in a single goroutine
// for the lifetime of the transport; Close() cancels its ctx.
//
// Reconnect policy: if the stream closes (server hung up, network
// blip), we wait briefly and reopen. Permanent failures (DNS gone,
// auth rejected, etc.) eventually surface as repeated reconnect
// attempts in slog; the transport doesn't surface them to the
// consumer because Subscribe is documented as best-effort.
func (t *httpTransport) startSSEListener() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.notifMu.Lock()
	t.sseCtx = ctx
	t.sseCancel = cancel
	t.sseDone = done
	t.notifMu.Unlock()

	go t.runSSEListener(ctx, done)
}

func (t *httpTransport) runSSEListener(ctx context.Context, done chan struct{}) {
	defer close(done)
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := t.readSSEStream(ctx); err != nil && ctx.Err() == nil {
			// Backoff before reconnecting. Capped exponential.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		// Clean EOF — reset backoff and reconnect quickly.
		backoff = time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// readSSEStream opens a single SSE connection and reads events until
// the server closes or ctx cancels. Each event is parsed as a
// JSON-RPC notification and dispatched to the current notifier.
// Returns the underlying error so runSSEListener can decide whether
// to reconnect with backoff.
func (t *httpTransport) readSSEStream(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if t.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.authToken)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sse: status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var dataBuf []byte
	dispatch := func() {
		if len(dataBuf) == 0 {
			return
		}
		payload := dataBuf
		dataBuf = nil
		var note rpcNotificationIn
		if err := json.Unmarshal(payload, &note); err != nil {
			return // Tolerate non-JSON heartbeats / comments.
		}
		if note.Method == "" {
			return
		}
		t.notifMu.Lock()
		handler := t.notifier
		t.notifMu.Unlock()
		if handler != nil {
			handler(note.Method, note.Params)
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			dispatch()
			continue
		}
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			// event: / id: / retry: / comments — ignored.
			continue
		}
		payload = strings.TrimPrefix(payload, " ")
		if len(dataBuf) > 0 {
			dataBuf = append(dataBuf, '\n')
		}
		dataBuf = append(dataBuf, payload...)
	}
	// Flush any trailing event with no terminating blank line.
	dispatch()
	return scanner.Err()
}

func (t *httpTransport) Call(ctx context.Context, req rpcRequest) (rpcResponse, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return rpcResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewReader(b))
	if err != nil {
		return rpcResponse{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if t.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+t.authToken)
	}

	resp, err := t.http.Do(httpReq)
	if err != nil {
		return rpcResponse{}, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return rpcResponse{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	return decodeHTTPResponse(resp.Header.Get("Content-Type"), resp.Body, req.ID)
}

// decodeHTTPResponse handles both plain JSON and Server-Sent Events
// responses. Streamable-HTTP MCP servers (e.g. DeepWiki) may respond
// with text/event-stream even for unary requests; we scan for the
// first matching-ID data: payload.
//
// wantID is the raw bytes of the originating request's id field —
// matching is done by bytes.Equal so String / Number / null IDs all
// match correctly (an integer id and the same digits as a string id
// were previously conflated when ID was a Go int).
func decodeHTTPResponse(contentType string, body io.Reader, wantID json.RawMessage) (rpcResponse, error) {
	if strings.Contains(contentType, "text/event-stream") {
		return decodeSSEResponse(body, wantID)
	}
	// Cap the JSON body: a malicious/compromised MCP server (connected at the
	// model's request) could otherwise stream a multi-GB response into a single
	// tools/call and OOM the client. The SSE path already caps its scanner.
	var r rpcResponse
	if err := json.NewDecoder(io.LimitReader(body, maxRPCBodyBytes)).Decode(&r); err != nil {
		return rpcResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return r, nil
}

func decodeRPCEvent(payload []byte) (rpcResponse, bool) {
	var r rpcResponse
	if err := json.Unmarshal(payload, &r); err != nil {
		return rpcResponse{}, false
	}
	return r, true
}

// decodeSSEResponse pulls one matching response out of an SSE
// stream. Multi-line `data:` continuations are concatenated per
// SSE spec — each event ends at a blank line — so a JSON payload
// split across lines parses correctly. Previously each line was
// json-unmarshalled independently, which silently dropped events
// whose payload exceeded one line.
func decodeSSEResponse(body io.Reader, wantID json.RawMessage) (rpcResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var dataBuf []byte
	flush := func() (rpcResponse, bool) {
		if len(dataBuf) == 0 {
			return rpcResponse{}, false
		}
		payload := dataBuf
		dataBuf = nil
		r, ok := decodeRPCEvent(payload)
		if !ok {
			// Tolerate junk events — e.g. heartbeats with non-JSON data —
			// rather than abort the whole stream.
			return rpcResponse{}, false
		}
		if bytes.Equal(r.ID, wantID) {
			return r, true
		}
		return rpcResponse{}, false
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// End of event — try to dispatch the accumulated data.
			if r, matched := flush(); matched {
				return r, nil
			}
			continue
		}
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			// Other SSE fields (event:, id:, retry:, comments) are
			// ignored — we only need the data payload for JSON-RPC.
			continue
		}
		// Per SSE spec, exactly one leading space after the colon is
		// stripped; subsequent leading spaces are kept. strings.CutPrefix
		// with "data: " (one space) is the conformant strip; falling
		// through to "data:" handles servers that omit the space.
		payload = strings.TrimPrefix(payload, " ")
		if len(dataBuf) > 0 {
			dataBuf = append(dataBuf, '\n')
		}
		dataBuf = append(dataBuf, payload...)
	}
	// Stream may end without a terminating blank line — flush any
	// pending payload.
	if r, matched := flush(); matched {
		return r, nil
	}
	if err := scanner.Err(); err != nil {
		return rpcResponse{}, fmt.Errorf("read sse stream: %w", err)
	}
	return rpcResponse{}, fmt.Errorf("sse stream closed without matching response for id %s", string(wantID))
}
