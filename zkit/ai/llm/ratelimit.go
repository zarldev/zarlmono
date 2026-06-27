package llm

import (
	"errors"
	"fmt"
	"time"
)

// RateLimitError carries retry-after/reset information from provider 429
// (or similar rate-limit) responses. Consumers (runner, TUI, zarlai HTTP
// handler) can type-assert the error from RunResult.Err with errors.As to
// render countdowns, back-pressure warnings, or billing notices.
type RateLimitError struct {
	Message    string
	RetryAfter time.Duration
	ResetAt    time.Time
	Permanent  bool
}

func (e *RateLimitError) Error() string {
	if e.Permanent {
		return fmt.Sprintf("rate limit (permanent): %s", e.Message)
	}
	if !e.ResetAt.IsZero() {
		return fmt.Sprintf("rate limit until %s: %s", e.ResetAt.Format(time.Kitchen), e.Message)
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limit (retry after %s): %s", e.RetryAfter, e.Message)
	}
	return fmt.Sprintf("rate limit: %s", e.Message)
}

func IsRateLimitError(err error) bool {
	_, ok := errors.AsType[*RateLimitError](err)
	return ok
}
