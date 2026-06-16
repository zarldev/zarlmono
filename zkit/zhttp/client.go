package zhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/options"
)

// Best-practices HTTP client for service-to-service / API calls.
//
// Why this exists: the codebase had 8+ ad-hoc `&http.Client{Timeout: ...}`
// constructions, each missing the transport-level timeouts the stdlib
// doesn't set by default and none implementing retry / backoff /
// Retry-After. A misbehaving upstream (TLS handshake hang, headers
// never sent, 503 with a Retry-After of 10s) used to surface as
// either a hang past the request deadline or a hard fail with no
// retry. This client closes those gaps:
//
//   - Transport with explicit Dial / TLSHandshake / ResponseHeader /
//     IdleConn / ExpectContinue timeouts.
//   - Retry on transient failures (connection errors + 408 / 429 /
//     5xx) with exponential backoff + jitter.
//   - Honors the Retry-After header (delta-seconds AND HTTP-date).
//   - Context-aware: every retry sleep respects ctx.Done().
//   - Body replay handled — request bodies must be nil, []byte, or
//     [io.ReadSeeker] so the client can rewind for each retry.
//   - slog hooks on every retry attempt + final failures, so
//     misbehaving upstreams are observable instead of silent.
//   - Optional [net/http/httptrace]-backed timing hooks for callers
//     debugging DNS / connect / TLS / first-byte / body-transfer
//     latency without forcing tracing onto every request.
//
// What's deliberately NOT here (build when there's a real consumer):
//
//   - Circuit breaker — needs per-host failure-count state machines;
//     orthogonal to retry policy.
//   - Rate limiting — server-side 429 + Retry-After already drives
//     the right behaviour; client-side limiter usually wrong layer.
//   - Auth / OAuth — caller sets request headers; client is
//     transport-agnostic.
//   - Metrics backend — callers decide whether measurements become slog
//     lines, Prometheus histograms, test assertions, or nothing.

// Client is a best-practices HTTP client. Safe for concurrent use
// from multiple goroutines — wraps an [http.Client] which itself is.
// Construct via [NewClient] with functional options.
type Client struct {
	httpClient *http.Client
	retry      RetryPolicy
	logger     *slog.Logger
	userAgent  string
	traceHook  TraceHook
}

// RetryPolicy governs how [Client.Do] re-issues a failed request.
// Zero-value fields fall back to defaults: 3 attempts (so 2 retries
// after the first), 100 ms initial backoff doubling each step up to
// 30 s, ±20 % jitter, and the standard transient-failure status set
// (408 / 429 / 500 / 502 / 503 / 504). Tune the policy per client
// when an upstream needs special treatment (idempotent-only,
// no-retry, longer backoff).
type RetryPolicy struct {
	// MaxAttempts caps total request attempts including the first.
	// 1 means "no retry"; 3 means "first try + 2 retries". Default 3.
	MaxAttempts int

	// InitialBackoff is the sleep before the first retry. Default 100ms.
	InitialBackoff time.Duration

	// MaxBackoff caps the per-attempt sleep regardless of how many
	// attempts have failed. Default 30s.
	MaxBackoff time.Duration

	// Multiplier scales the backoff between attempts (geometric
	// progression). Default 2.0.
	Multiplier float64

	// JitterFactor adds ±jitter to each sleep so a fleet of clients
	// retrying after a shared outage don't thundering-herd the
	// upstream. 0.2 means "±20% of the computed sleep". Default 0.2.
	JitterFactor float64

	// RetryableStatusCodes is the set of HTTP status codes that
	// trigger a retry. Default is the standard transient-failure
	// set; override for upstreams that use non-standard codes.
	RetryableStatusCodes []int

	// RetryNetworkErrors retries connection-level failures (refused,
	// reset, timeout). Default true. Set false when the caller wants
	// to handle network errors itself.
	RetryNetworkErrors bool

	// RespectRetryAfter honours the Retry-After response header on
	// 429 / 503 (delta-seconds or HTTP-date). When the server
	// suggests a wait longer than the computed backoff, we use the
	// server's value. Default true.
	RespectRetryAfter bool
}

// DefaultRetryPolicy returns the package-default policy. Use as a
// starting point and mutate fields rather than constructing from
// zero — the field defaults are not the Go zero values.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:          3,
		InitialBackoff:       100 * time.Millisecond,
		MaxBackoff:           30 * time.Second,
		Multiplier:           2.0,
		JitterFactor:         0.2,
		RetryableStatusCodes: []int{408, 429, 500, 502, 503, 504},
		RetryNetworkErrors:   true,
		RespectRetryAfter:    true,
	}
}

// NoRetry is the policy for "issue the request exactly once, never
// retry." Useful for endpoints with non-idempotent side effects the
// caller doesn't want to risk replaying.
func NoRetry() RetryPolicy {
	p := DefaultRetryPolicy()
	p.MaxAttempts = 1
	return p
}

// NewClient builds a Client with the default transport, default
// retry policy, and a 30s per-request timeout. Override via options.
func NewClient(opts ...options.Option[Client]) *Client {
	c := &Client{
		httpClient: &http.Client{
			Transport: DefaultTransport(),
			Timeout:   30 * time.Second,
		},
		retry:     DefaultRetryPolicy(),
		logger:    slog.Default(),
		userAgent: "zhttp/1",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithTimeout sets the per-request total timeout enforced by the
// underlying [http.Client]. Includes connect + headers + body read.
// Caller's ctx deadline is honoured independently — whichever fires
// first wins. Pass 0 to disable the client-side timeout entirely
// (then the only bound is the caller's ctx).
func WithTimeout(d time.Duration) options.Option[Client] {
	return func(c *Client) { c.httpClient.Timeout = d }
}

// WithTransport replaces the default transport. Useful for tests
// (httptest), for callers that need a custom dialer (Unix socket,
// SOCKS proxy), or for adding a wrapping RoundTripper for
// instrumentation. The replacement is used as-is — none of the
// timeouts [DefaultTransport] sets carry over.
func WithTransport(t http.RoundTripper) options.Option[Client] {
	return func(c *Client) { c.httpClient.Transport = t }
}

// WithRetryPolicy installs a custom retry policy. Pass [NoRetry] to
// disable retries entirely (the request runs once); pass a tweaked
// [DefaultRetryPolicy] for a tighter / looser schedule.
func WithRetryPolicy(p RetryPolicy) options.Option[Client] {
	return func(c *Client) { c.retry = p }
}

// WithLogger installs the slog instance used for retry-attempt and
// final-failure logs. Default is slog.Default(). Pass slog.New(
// slog.NewTextHandler(io.Discard, nil)) to silence the client.
func WithLogger(l *slog.Logger) options.Option[Client] {
	return func(c *Client) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithUserAgent sets the User-Agent header for every outgoing
// request. Default "zhttp/1". Empty string leaves the request's
// existing User-Agent intact (or unset).
func WithUserAgent(ua string) options.Option[Client] {
	return func(c *Client) { c.userAgent = ua }
}

// WithTraceHook installs an optional per-attempt timing hook backed by
// [net/http/httptrace]. The hook fires once per HTTP attempt:
// immediately for transport errors / responses without bodies, or when
// the response body is closed for responses returned to the caller.
// Retryable responses are drained and closed by [Client.Do], so their
// hooks fire before the next retry sleep.
//
// Existing ClientTrace hooks already attached to ctx are preserved —
// httptrace composes hooks when multiple traces are layered on the same
// context.
func WithTraceHook(h TraceHook) options.Option[Client] {
	return func(c *Client) { c.traceHook = h }
}

// DefaultTransport returns a transport with every timeout the stdlib
// leaves at "no limit" set to a sensible bound. Use directly when a
// caller wants the timeouts but not the retry / logging machinery
// of the Client wrapper.
//
// Values picked for a typical service-to-service API call. Lower
// them for tight intranet calls; raise for long-pole external APIs.
func DefaultTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second, // TCP connect
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, // server must start responding
		ExpectContinueTimeout: 1 * time.Second,  // 100-continue
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		ForceAttemptHTTP2:     true,
	}
}

// HTTPClient exposes the underlying [http.Client] for callers that
// need to reach an SDK expecting one (e.g. cloud SDKs that take a
// *http.Client). Modifying it after construction is unsupported —
// the Client's retry path assumes the http.Client is stable.
func (c *Client) HTTPClient() *http.Client { return c.httpClient }

// Do issues req with retry.
//
// Body replay: the stdlib's [http.NewRequest] populates
// [http.Request.GetBody] when body is a *bytes.Buffer,
// *bytes.Reader, or *strings.Reader — Do uses that factory to
// produce a fresh body for each retry. Bodies built any other way
// (io.Pipe, raw *os.File, etc.) leave GetBody nil and can't replay;
// in that case Do returns [ErrUnretryableBody] the moment a retry
// would fire. nil bodies (GET / HEAD / DELETE) skip body handling
// entirely.
//
// The returned response's Body is the caller's to close.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	policy := c.retry
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}

	// Remember whether retries can replay. Either there's no body
	// or the stdlib populated GetBody (it does so for the three
	// canonical types we recommend). Non-replayable bodies still
	// run the first attempt; we fail loudly only when a retry is
	// actually warranted.
	canReplay := req.Body == nil || req.GetBody != nil

	var lastResp *http.Response
	var lastErr error
	for attempt := range policy.MaxAttempts {
		// Honour ctx cancellation BEFORE issuing — saves a wasted
		// roundtrip when the caller has already given up.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Rewind body for replay. The first attempt uses the
		// caller-supplied req.Body unchanged; subsequent attempts
		// pull a fresh ReadCloser from req.GetBody.
		if attempt > 0 && req.Body != nil {
			if !canReplay {
				return nil, ErrUnretryableBody
			}
			fresh, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("rewind body for retry: %w", err)
			}
			req.Body = fresh
		}

		attemptCtx := ctx
		var attemptTrace *traceAttempt
		if c.traceHook != nil {
			attemptTrace = newTraceAttempt(attempt + 1)
			attemptCtx = attemptTrace.context(ctx)
		}

		resp, err := c.httpClient.Do(req.WithContext(attemptCtx))
		if c.traceHook != nil {
			c.installTraceFinalizer(ctx, req, attemptTrace, resp, err)
		}
		lastResp, lastErr = resp, err

		if !c.shouldRetry(resp, err, policy) {
			return resp, err
		}
		if attempt == policy.MaxAttempts-1 {
			// Out of attempts — return whatever we got last.
			break
		}

		// Drain + close the response body before retrying so the
		// connection can return to the pool. Skipping this leaks
		// the connection until the GC closes the response.
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		sleep := c.nextBackoff(attempt, resp, policy)
		c.logger.LogAttrs(ctx, slog.LevelInfo, "zhttp client: retrying",
			slog.String("method", req.Method),
			slog.String("url", req.URL.String()),
			slog.Int("attempt", attempt+1),
			slog.Int("max_attempts", policy.MaxAttempts),
			slog.Duration("sleep", sleep),
			slog.Any("err", err),
			slog.Int("status", statusOf(resp)),
		)

		// Sleep with ctx awareness so a cancelled caller doesn't
		// have to wait out the backoff.
		t := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}

	if lastErr != nil {
		c.logger.LogAttrs(ctx, slog.LevelWarn, "zhttp client: gave up after retries",
			slog.String("method", req.Method),
			slog.String("url", req.URL.String()),
			slog.Int("attempts", policy.MaxAttempts),
			slog.Any("err", lastErr),
		)
	}
	return lastResp, lastErr
}

// ErrUnretryableBody is returned by [Client.Do] when a retry would
// have fired but the request body can't be rewound. Pass a
// *bytes.Reader (or other io.ReadSeeker) to opt into retry on
// requests with bodies; otherwise the request is sent exactly once.
var ErrUnretryableBody = errors.New("zhttp client: retry impossible — request body is not seekable")

// shouldRetry decides whether a (resp, err) pair warrants another
// attempt. Network errors retry by default; status-code retry is
// driven by RetryableStatusCodes.
func (c *Client) shouldRetry(resp *http.Response, err error, p RetryPolicy) bool {
	if err != nil {
		if !p.RetryNetworkErrors {
			return false
		}
		// Don't retry context cancellation / deadline — the caller
		// asked us to stop. The net error path catches genuine
		// transport failures (refused, reset, DNS).
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		return true
	}
	if resp == nil {
		return false
	}
	for _, code := range p.RetryableStatusCodes {
		if resp.StatusCode == code {
			return true
		}
	}
	return false
}

// nextBackoff computes the sleep before the next attempt. Respects
// the server's Retry-After when present + the policy allows it;
// otherwise standard exponential backoff with jitter.
func (c *Client) nextBackoff(attempt int, resp *http.Response, p RetryPolicy) time.Duration {
	if p.RespectRetryAfter && resp != nil {
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			// Cap at MaxBackoff so a buggy server can't tell us to
			// sleep 24 hours. Negative / past dates → 0 → backoff
			// continues with the regular schedule.
			if d > p.MaxBackoff {
				d = p.MaxBackoff
			}
			if d > 0 {
				return d
			}
		}
	}
	// Exponential: base * mult^attempt, capped, with ±jitter.
	mult := p.Multiplier
	if mult <= 1 {
		mult = 2
	}
	base := float64(p.InitialBackoff)
	if base <= 0 {
		base = float64(100 * time.Millisecond)
	}
	d := base * math.Pow(mult, float64(attempt))
	maxD := float64(p.MaxBackoff)
	if maxD <= 0 {
		maxD = float64(30 * time.Second)
	}
	if d > maxD {
		d = maxD
	}
	if j := p.JitterFactor; j > 0 {
		// ±j fraction of d. rand.Float64() is fine here — we
		// don't need crypto-strength randomness for backoff jitter.
		d += d * j * (rand.Float64()*2 - 1)
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// parseRetryAfter accepts either delta-seconds ("120") or an
// HTTP-date ("Wed, 21 Oct 2026 07:28:00 GMT") and returns the
// duration to wait. Returns (0, false) on unparseable input or
// past dates so the caller falls back to the regular schedule.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, false
	}
	// Delta-seconds (most servers use this).
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	// HTTP-date.
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0, false
		}
		// Round to second so the log line is readable.
		return d.Round(time.Second), true
	}
	return 0, false
}

// statusOf returns the status code for slog, or 0 when resp is nil.
func statusOf(r *http.Response) int {
	if r == nil {
		return 0
	}
	return r.StatusCode
}

// --- JSON convenience helpers ---

// GetJSON issues a GET, expects a 2xx, and decodes the response
// body into out using [DecodeJSON]-like strict semantics (unknown
// fields rejected, body capped at [DefaultMaxBodyBytes]).
//
// Non-2xx responses surface as [*StatusError] so callers can switch
// on Code without parsing strings. The response body is closed
// before GetJSON returns.
func (c *Client) GetJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return c.doJSON(ctx, req, out)
}

// PostJSON marshals in as JSON, POSTs to url, and decodes the
// response body into out (or skips decoding when out is nil). The
// request body is a [*bytes.Reader] so retry is supported.
func (c *Client) PostJSON(ctx context.Context, url string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.ContentLength = int64(len(body))
	return c.doJSON(ctx, req, out)
}

// doJSON dispatches req, checks the status, and decodes JSON when
// out is non-nil. Shared by GetJSON / PostJSON.
func (c *Client) doJSON(ctx context.Context, req *http.Request, out any) error {
	resp, err := c.Do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read up to 4KB of body for the error message — enough to
		// surface a JSON {error: ...} payload without unbounded
		// allocation. The full body is dropped.
		const errBodyCap = 4 * 1024
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyCap))
		return &StatusError{
			Code:   resp.StatusCode,
			Status: resp.Status,
			Body:   strings.TrimSpace(string(excerpt)),
		}
	}
	if out == nil {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, DefaultMaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// StatusError carries a non-2xx response in a switchable form. The
// Body excerpt is capped at ~4KB so consumers can include it in
// logs / metrics without bringing down the host on a malicious
// upstream that returns gigabytes in error responses.
type StatusError struct {
	Code   int
	Status string
	Body   string
}

// Error renders "http <status>" (e.g. "http 503 Service Unavailable"),
// appending the trimmed response-body excerpt after a colon when one
// was captured.
func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("http %s", e.Status)
	}
	return fmt.Sprintf("http %s: %s", e.Status, e.Body)
}
