package zhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// fixture spins a test server whose handler increments an attempt
// counter and runs h on each call. Returns (URL, attempt counter).
func fixture(t *testing.T, h func(int, http.ResponseWriter, *http.Request)) (string, *int32) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := int(atomic.AddInt32(&n, 1))
		h(v, w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

// fastClient builds a Client with tiny backoffs so the test suite
// doesn't sit on time.Sleep.
func fastClient(t *testing.T, maxAttempts int) *zhttp.Client {
	t.Helper()
	p := zhttp.DefaultRetryPolicy()
	p.MaxAttempts = maxAttempts
	p.InitialBackoff = time.Millisecond
	p.MaxBackoff = 10 * time.Millisecond
	p.JitterFactor = 0
	return zhttp.NewClient(zhttp.WithRetryPolicy(p))
}

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	t.Parallel()
	url, n := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	})
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := fastClient(t, 3).Do(t.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(n); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on success)", got)
	}
}

func TestDo_RetriesOn503ThenSucceeds(t *testing.T) {
	t.Parallel()
	url, n := fixture(t, func(attempt int, w http.ResponseWriter, _ *http.Request) {
		if attempt < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := fastClient(t, 5).Do(t.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(n); got != 3 {
		t.Errorf("attempts = %d, want 3 (2 retries then success)", got)
	}
}

func TestDo_StopsAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	url, n := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := fastClient(t, 3).Do(t.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("final status = %d, want 502", resp.StatusCode)
	}
	if got := atomic.LoadInt32(n); got != 3 {
		t.Errorf("attempts = %d, want 3 (the cap)", got)
	}
}

func TestDo_HonorsRetryAfterSeconds(t *testing.T) {
	t.Parallel()
	var times []time.Time
	url, _ := fixture(t, func(attempt int, w http.ResponseWriter, _ *http.Request) {
		times = append(times, time.Now())
		if attempt == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	c := zhttp.NewClient(zhttp.WithRetryPolicy(zhttp.RetryPolicy{
		MaxAttempts:          3,
		InitialBackoff:       1 * time.Millisecond, // tiny — so we know Retry-After dominates
		MaxBackoff:           5 * time.Second,
		Multiplier:           2,
		JitterFactor:         0,
		RetryableStatusCodes: []int{429},
		RetryNetworkErrors:   true,
		RespectRetryAfter:    true,
	}))
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := c.Do(t.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if len(times) < 2 {
		t.Fatalf("expected ≥2 attempts, got %d", len(times))
	}
	gap := times[1].Sub(times[0])
	// Retry-After: 1s — allow ±200ms slack for scheduling.
	if gap < 800*time.Millisecond || gap > 1500*time.Millisecond {
		t.Errorf("gap between attempts = %v, want ~1s from Retry-After header", gap)
	}
}

func TestDo_NoRetryOnNon4xx5xx(t *testing.T) {
	t.Parallel()
	url, n := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, _ := fastClient(t, 5).Do(t.Context(), req)
	resp.Body.Close()
	if got := atomic.LoadInt32(n); got != 1 {
		t.Errorf("attempts = %d, want 1 (404 is not retryable)", got)
	}
}

func TestDo_ContextCancellationStopsRetry(t *testing.T) {
	t.Parallel()
	url, n := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	p := zhttp.DefaultRetryPolicy()
	p.MaxAttempts = 10
	p.InitialBackoff = 500 * time.Millisecond // larger than ctx — first retry sleep should be cut short
	c := zhttp.NewClient(zhttp.WithRetryPolicy(p))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	_, err := c.Do(ctx, req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	// 10 attempts would mean ctx wasn't honoured.
	if got := atomic.LoadInt32(n); got > 2 {
		t.Errorf("attempts = %d, expected ≤2 before ctx cancel", got)
	}
}

func TestDo_RetriesOnConnectionError(t *testing.T) {
	t.Parallel()
	url := "http://127.0.0.1:1" // port 1 — never listening
	c := fastClient(t, 3)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if _, err := c.Do(t.Context(), req); err == nil {
		t.Fatal("Do: want connection error, got nil")
	}
}

func TestDo_PostBodyReplayedAcrossRetries(t *testing.T) {
	t.Parallel()
	var bodies []string
	url, n := fixture(t, func(attempt int, w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if attempt < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	c := fastClient(t, 5)
	payload := `{"hello":"world"}`
	// http.NewRequestWithContext + *bytes.Reader populates req.GetBody
	// — that's what enables retry replay.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader([]byte(payload)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(t.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(n); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
	for i, b := range bodies {
		if b != payload {
			t.Errorf("attempt %d body = %q, want %q (rewind broken)", i+1, b, payload)
		}
	}
}

func TestGetJSON_DecodesIntoStruct(t *testing.T) {
	t.Parallel()
	type out struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	url, _ := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"alice","age":30}`)
	})
	var got out
	if err := fastClient(t, 1).GetJSON(t.Context(), url, &got); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if got.Name != "alice" || got.Age != 30 {
		t.Errorf("decoded = %+v, want {alice 30}", got)
	}
}

func TestGetJSON_NonStatusReturnsStatusError(t *testing.T) {
	t.Parallel()
	url, _ := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `not found`)
	})
	err := fastClient(t, 1).GetJSON(t.Context(), url, &struct{}{})
	if err == nil {
		t.Fatal("GetJSON: want error on 404, got nil")
	}
	var se *zhttp.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err is %T, want *zhttp.StatusError", err)
	}
	if se.Code != 404 {
		t.Errorf("StatusError.Code = %d, want 404", se.Code)
	}
}

func TestPostJSON_SendsBodyAndDecodes(t *testing.T) {
	t.Parallel()
	type reqShape struct {
		Hello string `json:"hello"`
	}
	type respShape struct {
		Echo string `json:"echo"`
	}
	url, _ := fixture(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		var got reqShape
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"echo":"`+got.Hello+`"}`)
	})
	var out respShape
	if err := fastClient(t, 1).PostJSON(t.Context(), url, reqShape{Hello: "world"}, &out); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if out.Echo != "world" {
		t.Errorf("Echo = %q, want world", out.Echo)
	}
}

// NoRetry honours the "exactly one attempt" contract: a 503 stays a
// 503 instead of being retried 3 times.
func TestNoRetryPolicy_NeverRetries(t *testing.T) {
	t.Parallel()
	url, n := fixture(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c := zhttp.NewClient(zhttp.WithRetryPolicy(zhttp.NoRetry()))
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, _ := c.Do(t.Context(), req)
	resp.Body.Close()
	if got := atomic.LoadInt32(n); got != 1 {
		t.Errorf("attempts = %d, want 1 (NoRetry)", got)
	}
}
