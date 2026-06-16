package zhttp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

func TestTraceHook_FiresWhenResponseBodyCloses(t *testing.T) {
	t.Parallel()
	url, _ := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	traces := make(chan zhttp.TraceTimings, 1)
	c := zhttp.NewClient(
		zhttp.WithRetryPolicy(zhttp.NoRetry()),
		zhttp.WithTraceHook(func(_ctx context.Context, _ *http.Request, timing zhttp.TraceTimings) {
			traces <- timing
		}),
	)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := c.Do(t.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	select {
	case timing := <-traces:
		t.Fatalf("trace fired before body close: %+v", timing)
	default:
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}

	timing := nextTrace(t, traces)
	if timing.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", timing.Attempt)
	}
	if timing.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", timing.StatusCode)
	}
	if timing.Err != nil {
		t.Errorf("Err = %v, want nil", timing.Err)
	}
	if timing.Total <= 0 {
		t.Errorf("Total = %v, want > 0", timing.Total)
	}
}

func TestTraceHook_FiresForEveryRetryAttempt(t *testing.T) {
	t.Parallel()
	url, _ := fixture(t, func(attempt int, w http.ResponseWriter, _ *http.Request) {
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	traces := make(chan zhttp.TraceTimings, 2)
	p := zhttp.DefaultRetryPolicy()
	p.MaxAttempts = 2
	p.InitialBackoff = time.Millisecond
	p.MaxBackoff = time.Millisecond
	p.JitterFactor = 0
	c := zhttp.NewClient(
		zhttp.WithRetryPolicy(p),
		zhttp.WithTraceHook(func(_ context.Context, _ *http.Request, timing zhttp.TraceTimings) {
			traces <- timing
		}),
	)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := c.Do(t.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	first := nextTrace(t, traces)
	if first.Attempt != 1 {
		t.Errorf("first Attempt = %d, want 1", first.Attempt)
	}
	if first.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("first StatusCode = %d, want 503", first.StatusCode)
	}

	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close final body: %v", err)
	}
	second := nextTrace(t, traces)
	if second.Attempt != 2 {
		t.Errorf("second Attempt = %d, want 2", second.Attempt)
	}
	if second.StatusCode != http.StatusOK {
		t.Errorf("second StatusCode = %d, want 200", second.StatusCode)
	}
}

func TestTraceHook_ComposesWithCallerClientTrace(t *testing.T) {
	t.Parallel()
	url, _ := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	var gotConn int32
	callerTrace := &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) {
			atomic.AddInt32(&gotConn, 1)
		},
	}
	traces := make(chan zhttp.TraceTimings, 1)
	c := zhttp.NewClient(
		zhttp.WithRetryPolicy(zhttp.NoRetry()),
		zhttp.WithTraceHook(func(_ context.Context, _ *http.Request, timing zhttp.TraceTimings) {
			traces <- timing
		}),
	)

	ctx := httptrace.WithClientTrace(t.Context(), callerTrace)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.Do(ctx, req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	_ = nextTrace(t, traces)
	if atomic.LoadInt32(&gotConn) == 0 {
		t.Fatal("caller ClientTrace GotConn did not fire")
	}
}

func TestTraceHook_FiresForTransportError(t *testing.T) {
	t.Parallel()
	traces := make(chan zhttp.TraceTimings, 1)
	c := zhttp.NewClient(
		zhttp.WithTransport(traceErrorTransport{}),
		zhttp.WithRetryPolicy(zhttp.NoRetry()),
		zhttp.WithTraceHook(func(_ context.Context, _ *http.Request, timing zhttp.TraceTimings) {
			traces <- timing
		}),
	)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com", nil)
	_, err := c.Do(t.Context(), req)
	if !errors.Is(err, errTraceTransport) {
		t.Fatalf("Do error = %v, want %v", err, errTraceTransport)
	}

	timing := nextTrace(t, traces)
	if timing.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", timing.Attempt)
	}
	if timing.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", timing.StatusCode)
	}
	if !errors.Is(timing.Err, errTraceTransport) {
		t.Fatalf("Err = %v, want %v", timing.Err, errTraceTransport)
	}
}

var errTraceTransport = errors.New("trace transport")

type traceErrorTransport struct{}

func (traceErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errTraceTransport
}

func nextTrace(t *testing.T, traces <-chan zhttp.TraceTimings) zhttp.TraceTimings {
	t.Helper()
	select {
	case timing := <-traces:
		return timing
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for trace hook")
		return zhttp.TraceTimings{}
	}
}
