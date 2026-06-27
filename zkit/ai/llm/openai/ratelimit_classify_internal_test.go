package openai

import (
	"errors"
	"testing"

	"github.com/openai/openai-go/v2"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestIsRateLimitAPIError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  *openai.Error
		want bool
	}{
		{"429 status", &openai.Error{StatusCode: 429}, true},
		{"context length 400", &openai.Error{StatusCode: 400, Code: "context_length_exceeded", Message: "This model's maximum context length is 8192 tokens"}, false},
		// Regression guard: the old substring heuristic flagged this because
		// "generate" contains "rate".
		{"generate failure 500", &openai.Error{StatusCode: 500, Message: "failed to generate response"}, false},
		{"server error", &openai.Error{StatusCode: 503, Message: "service unavailable"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRateLimitAPIError(tc.err); got != tc.want {
				t.Errorf("isRateLimitAPIError(%+v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsPermanentQuotaError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  *openai.Error
		want bool
	}{
		{"insufficient_quota type+code", &openai.Error{StatusCode: 429, Type: "insufficient_quota", Code: "insufficient_quota"}, true},
		{"transient rate limit", &openai.Error{StatusCode: 429, Code: "rate_limit_exceeded"}, false},
		{"plain 429", &openai.Error{StatusCode: 429}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isPermanentQuotaError(tc.err); got != tc.want {
				t.Errorf("isPermanentQuotaError(%+v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestUserFacingAPIError_RateLimitClassification(t *testing.T) {
	t.Parallel()

	// Transient 429 → rate-limit error, not permanent.
	transient := userFacingAPIError(&openai.Error{StatusCode: 429, Code: "rate_limit_exceeded", Message: "Rate limit reached"})
	rle, ok := errors.AsType[*llm.RateLimitError](transient)
	if !ok {
		t.Fatalf("transient 429: got %T, want *llm.RateLimitError", transient)
	}
	if rle.Permanent {
		t.Error("transient 429 should not be Permanent")
	}

	// Quota exhaustion → permanent.
	quota := userFacingAPIError(&openai.Error{StatusCode: 429, Type: "insufficient_quota", Code: "insufficient_quota", Message: "You exceeded your current quota"})
	if rle, ok := errors.AsType[*llm.RateLimitError](quota); !ok || !rle.Permanent {
		t.Errorf("quota: got (%v, %v), want a permanent rate-limit error", rle, ok)
	}

	// Ordinary failure → plain error, NOT a rate-limit error.
	if llm.IsRateLimitError(userFacingAPIError(&openai.Error{StatusCode: 500, Message: "failed to generate response"})) {
		t.Error("500 generate failure must not classify as a rate limit")
	}
}
