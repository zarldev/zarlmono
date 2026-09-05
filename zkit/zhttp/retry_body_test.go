package zhttp_test

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

type endlessBody struct{ closed atomic.Bool }

func (b *endlessBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, io.EOF
	}
	p[0] = 'x'
	return 1, nil
}
func (b *endlessBody) Close() error { b.closed.Store(true); return nil }

func TestRetryDoesNotDrainEndlessResponseBody(t *testing.T) {
	var attempts atomic.Int32
	first := &endlessBody{}
	client := zhttp.NewClient(
		zhttp.WithRetryPolicy(zhttp.NewRetryPolicy(2, time.Millisecond, time.Millisecond, zhttp.NoRetryJitter)),
		zhttp.WithTransport(retryTransport(func(*http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: first, Header: make(http.Header)}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody), Header: make(http.Header)}, nil
		})),
	)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		resp, err := client.Do(t.Context(), req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
	case <-time.After(time.Second):
		// Prevent an old draining implementation from leaving a test goroutine behind.
		first.closed.Store(true)
		t.Fatal("retry stalled draining an endless response body")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if !first.closed.Load() {
		t.Fatal("retry response body was not closed")
	}
}
