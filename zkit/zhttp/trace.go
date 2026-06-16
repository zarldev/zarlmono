package zhttp

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

// TraceHook receives one completed HTTP-attempt timing snapshot from a
// [Client]. Hooks run synchronously on the goroutine that observed
// completion: usually the caller's body-close path, or Client.Do's retry
// drain / transport-error path. Keep hooks fast; hand off to another
// goroutine if exporting to a slow sink.
type TraceHook func(ctx context.Context, req *http.Request, t TraceTimings)

// TraceTimings is the timing breakdown for one HTTP attempt.
//
// Durations are zero when a phase did not happen or was not observable:
// reused connections skip DNS / TCP / TLS, plain HTTP skips TLS, and a
// response with no first byte leaves TimeToFirstByte / ServerProcessing
// at zero.
type TraceTimings struct {
	// Attempt is 1 for the first try, 2 for the first retry, and so on.
	Attempt int

	// StatusCode is the HTTP status code, or zero when the attempt ended
	// before a response was available.
	StatusCode int

	// Err is the transport error returned by net/http, if any. HTTP 4xx
	// and 5xx statuses are responses, not transport errors, so Err stays
	// nil for those.
	Err error

	// DNSLookup is the resolver phase duration.
	DNSLookup time.Duration

	// TCPConnect is the TCP dial duration.
	TCPConnect time.Duration

	// TLSHandshake is the TLS handshake duration for HTTPS requests.
	TLSHandshake time.Duration

	// TimeToFirstByte is measured from attempt start until the first
	// response byte arrives.
	TimeToFirstByte time.Duration

	// ServerProcessing is measured from connection acquisition until the
	// first response byte arrives. It excludes DNS / TCP / TLS setup for
	// new connections.
	ServerProcessing time.Duration

	// Total is measured from attempt start until the response body is
	// closed, or until the transport error is observed.
	Total time.Duration

	// ConnReused reports whether net/http reused an existing connection.
	ConnReused bool

	// ConnWasIdle reports whether the reused connection came from the
	// idle pool.
	ConnWasIdle bool

	// ConnIdleTime reports how long an idle reused connection sat in the
	// pool before this attempt acquired it.
	ConnIdleTime time.Duration
}

type traceAttempt struct {
	mu sync.Mutex

	attempt int
	start   time.Time

	dnsStart     time.Time
	dnsDone      time.Time
	connectStart time.Time
	connectDone  time.Time
	tlsStart     time.Time
	tlsDone      time.Time
	gotConn      time.Time
	firstByte    time.Time

	connReused   bool
	connWasIdle  bool
	connIdleTime time.Duration
}

func newTraceAttempt(attempt int) *traceAttempt {
	return &traceAttempt{attempt: attempt, start: time.Now()}
}

func (a *traceAttempt) context(ctx context.Context) context.Context {
	trace := &httptrace.ClientTrace{
		DNSStart: func(_ httptrace.DNSStartInfo) {
			a.record(func() { a.dnsStart = time.Now() })
		},
		DNSDone: func(_ httptrace.DNSDoneInfo) {
			a.record(func() { a.dnsDone = time.Now() })
		},
		ConnectStart: func(_, _ string) {
			a.record(func() { a.connectStart = time.Now() })
		},
		ConnectDone: func(_, _ string, _ error) {
			a.record(func() { a.connectDone = time.Now() })
		},
		TLSHandshakeStart: func() {
			a.record(func() { a.tlsStart = time.Now() })
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			a.record(func() { a.tlsDone = time.Now() })
		},
		GotConn: func(info httptrace.GotConnInfo) {
			a.record(func() {
				a.gotConn = time.Now()
				a.connReused = info.Reused
				a.connWasIdle = info.WasIdle
				a.connIdleTime = info.IdleTime
			})
		},
		GotFirstResponseByte: func() {
			a.record(func() { a.firstByte = time.Now() })
		},
	}
	return httptrace.WithClientTrace(ctx, trace)
}

func (a *traceAttempt) record(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn()
}

func (a *traceAttempt) finish(status int, err error, done time.Time) TraceTimings {
	a.mu.Lock()
	defer a.mu.Unlock()

	t := TraceTimings{
		Attempt:      a.attempt,
		StatusCode:   status,
		Err:          err,
		Total:        done.Sub(a.start),
		ConnReused:   a.connReused,
		ConnWasIdle:  a.connWasIdle,
		ConnIdleTime: a.connIdleTime,
	}
	if observed(a.dnsStart, a.dnsDone) {
		t.DNSLookup = a.dnsDone.Sub(a.dnsStart)
	}
	if observed(a.connectStart, a.connectDone) {
		t.TCPConnect = a.connectDone.Sub(a.connectStart)
	}
	if observed(a.tlsStart, a.tlsDone) {
		t.TLSHandshake = a.tlsDone.Sub(a.tlsStart)
	}
	if !a.firstByte.IsZero() {
		t.TimeToFirstByte = a.firstByte.Sub(a.start)
		if !a.gotConn.IsZero() {
			t.ServerProcessing = a.firstByte.Sub(a.gotConn)
		}
	}
	return t
}

func observed(start, done time.Time) bool {
	return !start.IsZero() && !done.IsZero()
}

func (c *Client) installTraceFinalizer(ctx context.Context, req *http.Request, attempt *traceAttempt, resp *http.Response, err error) {
	if resp == nil || resp.Body == nil {
		c.emitTrace(ctx, req, attempt, resp, err, time.Now())
		return
	}
	resp.Body = &traceBody{
		ReadCloser: resp.Body,
		close: func() {
			c.emitTrace(ctx, req, attempt, resp, err, time.Now())
		},
	}
}

func (c *Client) emitTrace(ctx context.Context, req *http.Request, attempt *traceAttempt, resp *http.Response, err error, done time.Time) {
	if c.traceHook == nil {
		return
	}
	c.traceHook(ctx, req, attempt.finish(statusOf(resp), err, done))
}

type traceBody struct {
	io.ReadCloser
	once  sync.Once
	close func()
}

func (b *traceBody) Close() error {
	var err error
	b.once.Do(func() {
		err = b.ReadCloser.Close()
		b.close()
	})
	return err
}
