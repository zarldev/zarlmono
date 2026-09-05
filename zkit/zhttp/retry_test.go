package zhttp_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

type retryTransport func(*http.Request) (*http.Response, error)

func (f retryTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRetryMethodEligibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method string
		want   int
	}{
		{http.MethodGet, 3}, {http.MethodHead, 3}, {http.MethodPut, 3},
		{http.MethodDelete, 3}, {http.MethodOptions, 3}, {http.MethodTrace, 3},
		{http.MethodPost, 1}, {http.MethodPatch, 1}, {"CUSTOM", 1}, {"", 3},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			for _, transportError := range []bool{false, true} {
				attempts := 0
				client := zhttp.NewClient(
					zhttp.WithRetryPolicy(zhttp.NewRetryPolicy(3, time.Millisecond, time.Millisecond, zhttp.NoRetryJitter)),
					zhttp.WithTransport(retryTransport(func(req *http.Request) (*http.Response, error) {
						attempts++
						if req.Body != nil {
							_ = req.Body.Close()
						}
						if transportError {
							return nil, errors.New("response lost")
						}
						return &http.Response{
							StatusCode: http.StatusServiceUnavailable,
							Body:       io.NopCloser(strings.NewReader("unavailable")),
							Header:     make(http.Header),
						}, nil
					})),
				)
				req, err := http.NewRequestWithContext(t.Context(), tc.method, "http://example.test", strings.NewReader("replayable"))
				if err != nil {
					t.Fatal(err)
				}
				resp, err := client.Do(t.Context(), req)
				if (err != nil) != transportError {
					t.Fatalf("transportError=%v: error=%v", transportError, err)
				}
				if resp != nil {
					data, err := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if err != nil || string(data) != "unavailable" {
						t.Fatalf("final body = %q, %v", data, err)
					}
				}
				if attempts != tc.want {
					t.Fatalf("transportError=%v: attempts=%d, want %d", transportError, attempts, tc.want)
				}
			}
		})
	}
}

func TestRetryMethodOptInRequiresReplayableBody(t *testing.T) {
	t.Parallel()
	for _, optIn := range []bool{false, true} {
		attempts := 0
		client := zhttp.NewClient(
			zhttp.WithRetryPolicy(zhttp.NewRetryPolicy(3, time.Millisecond, time.Millisecond, zhttp.NoRetryJitter)),
			zhttp.WithTransport(retryTransport(func(req *http.Request) (*http.Response, error) {
				attempts++
				_ = req.Body.Close()
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Body:       io.NopCloser(strings.NewReader("")),
					Header:     make(http.Header),
				}, nil
			})),
			zhttp.WithRetryMethods(retryOptInMethods(optIn)...),
		)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.test", io.NopCloser(strings.NewReader("not replayable")))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(t.Context(), req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if optIn && !errors.Is(err, zhttp.ErrUnretryableBody) {
			t.Fatalf("opted-in error = %v, want ErrUnretryableBody", err)
		}
		if !optIn && err != nil {
			t.Fatalf("disabled retries error = %v", err)
		}
		if attempts != 1 {
			t.Fatalf("attempts=%d, want 1", attempts)
		}
	}
}

func retryOptInMethods(enabled bool) []string {
	if enabled {
		return []string{http.MethodPost}
	}
	return nil
}
