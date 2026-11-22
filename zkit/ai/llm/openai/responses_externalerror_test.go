package openai_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

func TestResponsesStructuredError(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body, code   string
		stream             bool
		status, wantStatus int
		retryAfter         time.Duration
	}{
		{name: "stream error", stream: true, status: 200, body: `data: {"type":"error","code":"invalid_api_key","message":"native diagnostic"}` + "\n\n", code: "invalid_api_key"},
		{name: "stream failed", stream: true, status: 200, body: `data: {"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"Rate limit reached. Please try again in 2s."}}}` + "\n\n", code: "rate_limit_exceeded", retryAfter: 2 * time.Second},
		{name: "JSON error", status: 200, body: `{"status":"failed","error":{"code":"invalid_api_key","message":"native diagnostic"}}`, code: "invalid_api_key"},
		{name: "HTTP error", status: 401, wantStatus: 401, body: `{"error":{"code":"invalid_api_key","message":"native diagnostic"}}`, code: "invalid_api_key"},
		{name: "HTTP rate", status: 429, wantStatus: 429, body: `{"error":{"code":"rate_limit_exceeded","message":"Rate limit reached"}}`, code: "rate_limit_exceeded", retryAfter: 2 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if test.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				if test.status == 429 {
					w.Header().Set("Retry-After", "2")
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)
			provider := openai.NewProvider("test-key", openai.WithBaseURL(server.URL), openai.WithResponsesAPI(true))
			req := providertest.SimpleRequest("hi")
			req.Stream = test.stream
			var terminal error
			for _, err := range provider.Complete(t.Context(), req) {
				if terminal != nil {
					t.Fatal("yield after terminal error")
				}
				if err != nil {
					terminal = err
				}
			}
			var response *openai.ResponsesError
			if !errors.As(terminal, &response) {
				t.Fatalf("structured error missing: %v", terminal)
			}
			if response.Code != test.code || response.StatusCode != test.wantStatus {
				t.Fatalf("code/status = %q/%d", response.Code, response.StatusCode)
			}
			if test.retryAfter > 0 {
				var rate *llm.RateLimitError
				if !errors.As(terminal, &rate) || rate.RetryAfter != test.retryAfter || !rate.Retryable {
					t.Fatalf("rate details = %+v (%v)", rate, terminal)
				}
			}
		})
	}
}
