package anthropic_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestRateLimitPreservesProviderResetAndRetryPolicy(t *testing.T) {
	t.Parallel()
	reset := time.Date(2027, time.January, 2, 3, 4, 5, 0, time.UTC)
	for _, stream := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Should-Retry", "false") // Exercise adapter policy, not SDK sleep/retry.
			w.Header().Set("Retry-After", "3")
			w.Header().Set("Anthropic-Ratelimit-Tokens-Reset", reset.Format(time.RFC3339))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"synthetic throttle"}}`))
		}))
		provider := anthropic.NewProvider("synthetic-key", anthropic.WithBaseURL(server.URL), anthropic.WithHTTPClient(server.Client()))
		failures := 0
		for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: stream, Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}}) {
			if err == nil {
				t.Error("rate limit emitted successful data")
				continue
			}
			failures++
			var limited *llm.RateLimitError
			if !errors.As(err, &limited) || !limited.Retryable || limited.RetryAfter != 3*time.Second || !limited.ResetAt.Equal(reset) {
				t.Errorf("rate-limit metadata=%+v err=%v", limited, err)
			}
			if chunk.Content != "" || chunk.UsageReported {
				t.Error("terminal error has data")
			}
		}
		server.Close()
		if failures != 1 {
			t.Fatalf("terminal failures=%d", failures)
		}
	}
}
