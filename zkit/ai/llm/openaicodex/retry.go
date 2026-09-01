package openaicodex

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// defaultRetryPolicy returns the policy applied when the caller
// constructs a provider without supplying [WithRetryPolicy] or
// [WithNoRetry]. Defaults: 4 attempts (first try + 3 retries), 1s
// base sleep doubling per attempt, 30s cap; honours Retry-After on
// 429 / 5xx (always more accurate than our exponential guess when
// the server tells us how long to wait); no jitter (kept off so
// tests with `Retry-After: 0` are deterministic — the production
// cost on a single-backend retry budget is negligible).
func defaultRetryPolicy() zhttp.RetryPolicy {
	return zhttp.NewRetryPolicy(4, time.Second, 30*time.Second, zhttp.NoRetryJitter)
}

// WithRetryPolicy tunes how aggressively the provider retries transient HTTP
// failures. maxAttempts must be between 1 and 8 inclusive, base must be
// positive, and baseCap must be at least base. Invalid values panic when the
// option is applied. Retryable status codes follow the [zhttp] defaults: 408,
// 429, 500, 502, 503, and 504.
func WithRetryPolicy(maxAttempts int, base, baseCap time.Duration) options.Option[Provider] {
	return func(p *Provider) {
		if maxAttempts > 8 {
			panic("openaicodex: retry policy max attempts must not exceed 8")
		}
		p.retryPolicy = zhttp.NewRetryPolicy(maxAttempts, base, baseCap, zhttp.NoRetryJitter)
	}
}

// WithNoRetry disables retries; the next 429 / 5xx surfaces
// immediately. Tests use this to avoid waiting through exponential
// backoff when the failure is the point of the test.
func WithNoRetry() options.Option[Provider] {
	return func(p *Provider) { p.retryPolicy = zhttp.RetryPolicy{} }
}
