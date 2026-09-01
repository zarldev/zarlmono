package openai_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestProviderClassifiesHTTPError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		status     int
		wantRate   bool
		permanent  bool
	}{
		{name: "transient 429", status: http.StatusTooManyRequests, body: `{"error":{"message":"Rate limit reached","code":"rate_limit_exceeded"}}`, wantRate: true},
		{name: "quota exhausted", status: http.StatusTooManyRequests, body: `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`, wantRate: true, permanent: true},
		{name: "context length 400", status: http.StatusBadRequest, body: `{"error":{"message":"maximum context length","code":"context_length_exceeded"}}`},
		{name: "generate failure 500", status: http.StatusInternalServerError, body: `{"error":{"message":"failed to generate response"}}`},
		{name: "server error", status: http.StatusServiceUnavailable, body: `{"error":{"message":"service unavailable"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			p, err := openai.NewProvider("test-key", openai.WithBaseURL(server.URL))
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			var got error
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true}) {
				if err != nil {
					got = err
				}
			}
			var rle *llm.RateLimitError
			isRate := errors.As(got, &rle)
			if isRate != tc.wantRate {
				t.Fatalf("error = %T %v, rate limit = %v, want %v", got, got, isRate, tc.wantRate)
			}
			if isRate && rle.Permanent != tc.permanent {
				t.Fatalf("Permanent = %v, want %v", rle.Permanent, tc.permanent)
			}
		})
	}
}
