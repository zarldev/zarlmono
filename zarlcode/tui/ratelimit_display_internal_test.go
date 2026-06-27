package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestFormatRateLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   *llm.RateLimitError
		want string
	}{
		{
			name: "permanent",
			in:   &llm.RateLimitError{Message: "quota exhausted", Permanent: true},
			want: "usage limit reached: quota exhausted",
		},
		{
			name: "retry after short",
			in:   &llm.RateLimitError{Message: "slow down", RetryAfter: 30 * time.Second},
			want: "rate limit — retry in 30s: slow down",
		},
		{
			name: "retry after hours and minutes",
			in:   &llm.RateLimitError{Message: "wait", RetryAfter: 90 * time.Minute},
			want: "rate limit — retry in 1h30m: wait",
		},
		{
			name: "plain, no timing",
			in:   &llm.RateLimitError{Message: "too many requests"},
			want: "rate limit: too many requests",
		},
		{
			name: "no message",
			in:   &llm.RateLimitError{RetryAfter: 5 * time.Second},
			want: "rate limit — retry in 5s",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatRateLimit(tc.in); got != tc.want {
				t.Errorf("formatRateLimit() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A far-future reset (the Codex usage-limit shape) renders as a humanized
// duration, not a misleading bare clock time, and never leaks raw JSON.
func TestFormatRateLimit_FarFutureReset(t *testing.T) {
	t.Parallel()
	e := &llm.RateLimitError{
		Message: "The usage limit has been reached",
		ResetAt: time.Now().Add(56 * time.Hour),
	}
	got := formatRateLimit(e)
	if !strings.HasPrefix(got, "rate limit — resets in 2 days") {
		t.Errorf("got %q, want a humanized multi-day reset", got)
	}
	if !strings.HasSuffix(got, ": The usage limit has been reached") {
		t.Errorf("got %q, want the human message appended", got)
	}
}

// A reset within the day renders as a clock time.
func TestFormatRateLimit_SoonReset(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(90 * time.Minute)
	e := &llm.RateLimitError{Message: "back soon", ResetAt: reset}
	want := "rate limit — resets at " + reset.Format(time.Kitchen) + ": back soon"
	if got := formatRateLimit(e); got != want {
		t.Errorf("formatRateLimit() = %q, want %q", got, want)
	}
}
