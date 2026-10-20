package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	baseURL               string
	authToken             string
	http                  *http.Client
	protocolMu            sync.Mutex
	version               string
	session               string
	headerBindings        map[string][]schemaHeaderBinding
	invalidHeaderBindings map[string]bool
	headerBindingsReady   bool
	ownerContext          context.Context
	ownerCancel           context.CancelCauseFunc
	active                sync.WaitGroup
	closeDone             chan struct{}
	ensureReady           func(context.Context) error
	invalidateSession     func(string)

	// Push-notification plumbing. notifMu serialises (notifier,
	// sseCancel, sseDone) — registering a handler, replacing it, and
	// teardown via Close all touch these together. The SSE goroutine
	// is started at most once per transport lifetime.
	notifMu   sync.Mutex
	notifier  func(method string, params json.RawMessage)
	closed    bool
	sseStart  sync.Once // lazy start gate
	sseCancel context.CancelFunc
	sseDone   chan struct{}
}

func newHTTPTransport(baseURL, authToken string, policy AddrPolicy, confineEndpoint bool) *httpTransport {
	tr := zhttp.DefaultTransport()
	if policy != nil {
		tr.DialContext = validatingDialContext(policy)
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("MCP endpoint redirects are not allowed")
	}}
	if confineEndpoint {
		// Proxies can bypass confinement even when redirects are disabled.
		tr.Proxy = nil
	}
	ownerContext, ownerCancel := context.WithCancelCause(context.Background())
	return &httpTransport{
		baseURL:      baseURL,
		authToken:    authToken,
		ownerContext: ownerContext,
		ownerCancel:  ownerCancel,
		closeDone:    make(chan struct{}),
		// SSE wants no total-request timeout — the stream is meant to
		// stay open. We rely on per-call timeouts in Do() instead by
		// using a context with deadline on POST requests, and use zhttp's
		// hardened transport for Dial / TLS / Header / Idle timeouts.
		http: client,
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
	if t.closed {
		t.notifMu.Unlock()
		<-t.closeDone
		return nil
	}
	t.closed = true
	t.ownerCancel(errors.New("MCP HTTP transport closed"))
	cancel := t.sseCancel
	done := t.sseDone
	t.notifMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	t.active.Wait()
	t.closeSession()
	t.http.CloseIdleConnections()
	close(t.closeDone)
	return nil
}

// OnNotification implements [NotificationReceiver]. The first call
// kicks off the SSE listener goroutine; subsequent calls just swap
// the handler. Pass nil to clear the handler (notifications still
// arrive on the wire but are dropped).
func (t *httpTransport) OnNotification(handler func(method string, params json.RawMessage)) {
	t.notifMu.Lock()
	defer t.notifMu.Unlock()
	if t.closed {
		return
	}
	t.notifier = handler
	t.sseStart.Do(t.startSSEListenerLocked)
}

// startSSEListenerLocked requires t.notifMu to be held. Starting and closing
// are serialized by that lock, so Close winning the race prevents a listener
// from being created afterward.
func (t *httpTransport) startSSEListenerLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.sseCancel = cancel
	t.sseDone = done
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
			if errors.Is(err, ErrSSEEventTooLarge) {
				slog.WarnContext(ctx, "mcp: SSE listener stopped", "error", err)
				return
			}
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
	req, err := t.subscriptionRequest(ctx)
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
	if resp.StatusCode == http.StatusNotFound && req.Header.Get(headerSessionID) != "" && t.invalidateSession != nil {
		t.invalidateSession(req.Header.Get(headerSessionID))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sse: status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), maxSSELineBytes)
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
		if req.Method == http.MethodPost && !validSubscriptionNotification(note.Params, json.RawMessage(`"subscription"`)) {
			return
		}
		if note.Method == methodSubscriptionAcknowledged {
			return
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
		dataBuf, err = appendSSEData(dataBuf, payload, maxRPCBodyBytes)
		if err != nil {
			return err
		}
	}
	if err := sseScanError(scanner.Err()); err != nil {
		return err
	}
	// Flush any trailing event with no terminating blank line at clean EOF.
	dispatch()
	return nil
}

func (t *httpTransport) Call(ctx context.Context, req rpcRequest) (rpcResponse, error) {
	t.notifMu.Lock()
	if t.closed {
		t.notifMu.Unlock()
		return rpcResponse{}, errors.New("MCP HTTP transport closed")
	}
	t.active.Add(1)
	t.notifMu.Unlock()
	defer t.active.Done()
	callContext, cancel := context.WithCancelCause(ctx)
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(t.ownerContext, func() {
		defer close(callbackDone)
		cancel(context.Cause(t.ownerContext))
	})
	defer func() {
		cancel(nil)
		if !stop() {
			<-callbackDone
		}
	}()
	ctx = callContext
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
	if err := t.protocolHeaders(httpReq, req); err != nil {
		return rpcResponse{}, err
	}

	resp, err := t.http.Do(httpReq)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return rpcResponse{}, cause
		}
		return rpcResponse{}, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRPCBodyBytes))
		var failure rpcResponse
		_ = json.Unmarshal(body, &failure)
		failureError := &httpStatusError{Status: resp.StatusCode, RPC: failure.Error}
		if resp.StatusCode == http.StatusNotFound && httpReq.Header.Get(headerSessionID) != "" && t.invalidateSession != nil {
			t.invalidateSession(httpReq.Header.Get(headerSessionID))
		}
		// Keep status-driven session invalidation, but do not hide cancellation
		// behind the status when it interrupted reading the error body.
		if readErr != nil {
			if cause := context.Cause(ctx); cause != nil {
				return rpcResponse{}, cause
			}
		}
		return rpcResponse{}, failureError
	}

	if req.IsNotification() {
		return rpcResponse{}, notificationResponse(resp)
	}
	var decoded rpcResponse
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		decoded, err = decodeSSEResponse(resp.Body, req.ID, t.dispatchHTTPNotification)
	} else {
		decoded, err = decodeHTTPResponse(resp.Header.Get("Content-Type"), resp.Body, req.ID)
	}
	if err != nil {
		return rpcResponse{}, err
	}
	if decoded.JSONRPC != jsonrpcVersion || string(decoded.ID) != string(req.ID) {
		return rpcResponse{}, errors.New("MCP response envelope/id mismatch")
	}
	if req.Method == methodInitialize && decoded.Error == nil {
		result, validateErr := validateInitializeResult(decoded.Result)
		if validateErr != nil {
			return rpcResponse{}, validateErr
		}
		if result.ProtocolVersion != protocolVersion20251125 {
			return rpcResponse{}, fmt.Errorf("unsupported MCP HTTP initialization revision %q", result.ProtocolVersion)
		}
		if headerVersion := resp.Header.Get(headerProtocolVersion); headerVersion != "" && headerVersion != result.ProtocolVersion {
			return rpcResponse{}, fmt.Errorf("MCP initialize response protocol header %q does not match %q", headerVersion, result.ProtocolVersion)
		}
		if err := t.acceptSession(resp); err != nil {
			return rpcResponse{}, err
		}
	}
	return decoded, nil
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
	// Read one extra byte so the cap covers the complete JSON document, not
	// merely its first value. A decoder alone accepts a valid object followed
	// by a second value, which is not a JSON-RPC response body.
	payload, err := io.ReadAll(io.LimitReader(body, maxRPCResponseBytes+1))
	if err != nil {
		return rpcResponse{}, fmt.Errorf("read response: %w", err)
	}
	if len(payload) > maxRPCResponseBytes {
		return rpcResponse{}, fmt.Errorf("decode response: body exceeds %d bytes", maxRPCResponseBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	var r rpcResponse
	if err := dec.Decode(&r); err != nil {
		return rpcResponse{}, fmt.Errorf("decode response: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return rpcResponse{}, errors.New("decode response: trailing JSON value")
		}
		return rpcResponse{}, fmt.Errorf("decode response: trailing content: %w", err)
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
func decodeSSEResponse(body io.Reader, wantID json.RawMessage, notifications ...func([]byte)) (rpcResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxRPCResponseBytes+len("data: \r\n")+1)
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
		if len(r.ID) == 0 && len(notifications) != 0 {
			notifications[0](payload)
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
		var err error
		dataBuf, err = appendSSEData(dataBuf, payload, maxRPCResponseBytes)
		if err != nil {
			return rpcResponse{}, err
		}
	}
	if err := sseScanError(scanner.Err()); err != nil {
		return rpcResponse{}, fmt.Errorf("read sse stream: %w", err)
	}
	// Stream may end without a terminating blank line — flush any
	// pending payload at clean EOF.
	if r, matched := flush(); matched {
		return r, nil
	}
	return rpcResponse{}, fmt.Errorf("sse stream closed without matching response for id %s", string(wantID))
}
