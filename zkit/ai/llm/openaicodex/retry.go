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
	policy := zhttp.DefaultRetryPolicy()
	policy.MaxAttempts = 4
	policy.InitialBackoff = time.Second
	policy.MaxBackoff = 30 * time.Second
	policy.JitterFactor = 0
	return policy
}

// WithRetryPolicy tunes how aggressively the provider retries
// transient HTTP failures. maxAttempts <= 1 disables retries; values
// over 8 are clamped to 8 to keep a runaway server-side outage from
// pinning a goroutine for minutes. Retryable status codes follow
// the [zhttp] defaults: 408, 429, 500, 502, 503, 504 — the
// principled "transient" set rather than blanket 5xx (501 / 505 are
// non-transient configuration faults and shouldn't be retried).
func WithRetryPolicy(maxAttempts int, base, baseCap time.Duration) options.Option[Provider] {
	return func(p *Provider) {
		if maxAttempts < 1 {
			maxAttempts = 1
		}
		if maxAttempts > 8 {
			maxAttempts = 8
		}
		if base <= 0 {
			base = time.Second
		}
		if baseCap < base {
			baseCap = base
		}
		policy := defaultRetryPolicy()
		policy.MaxAttempts = maxAttempts
		policy.InitialBackoff = base
		policy.MaxBackoff = baseCap
		p.client = newCodexClient(policy)
	}
}

// WithNoRetry disables retries; the next 429 / 5xx surfaces
// immediately. Tests use this to avoid waiting through exponential
// backoff when the failure is the point of the test.
func WithNoRetry() options.Option[Provider] {
	return func(p *Provider) { p.client = newCodexClient(zhttp.NoRetry()) }
}
