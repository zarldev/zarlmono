package openaicodex_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestQuotaClassificationUsesBodyAndHeaderTiming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, header, value string
		permanent                 bool
	}{
		{"header retry", `{"error":{"type":"usage_limit_reached"}}`, "Retry-After", "60", false},
		{"header reset", `{"error":{"type":"usage_limit_reached"}}`, "X-Ratelimit-Reset", "4102444800", false},
		{"no reset", `{"error":{"type":"usage_limit_reached"}}`, "", "", false},
		{"billing", `{"error":{"type":"insufficient_quota"}}`, "Retry-After", "60", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.header != "" {
					w.Header().Set(tc.header, tc.value)
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(server.Close)
			p := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: freshToken(t, "fixture")}, openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry())
			var got error
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}, Stream: true}) {
				if err != nil {
					got = err
				}
			}
			limit, ok := errors.AsType[*llm.RateLimitError](got)
			if !ok {
				t.Fatalf("got %v", got)
			}
			if limit.Permanent != tc.permanent {
				t.Fatalf("permanent=%v, want %v", limit.Permanent, tc.permanent)
			}
			if tc.header == "Retry-After" && limit.RetryAfter != time.Minute {
				t.Fatalf("retry=%v", limit.RetryAfter)
			}
			if tc.header == "X-Ratelimit-Reset" && !limit.ResetAt.Equal(time.Unix(4102444800, 0)) {
				t.Fatal("reset hint lost")
			}
		})
	}
}
