package runner

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// TaskTiming measures non-overlapping phases of one Run, excluding descendants.
// ProviderDuration includes CallbackDuration; never add those two together.
// Unmeasured work (compaction, backoff, history settlement, event publication,
// conversation waits, and setup failures) remains in the task duration residual.
// These are monotonic elapsed times, not server-only inference measurements.
type TaskTiming struct {
	// ProviderAttempts counts invoked streams, including failures and retries.
	ProviderAttempts int `json:"provider_attempts"`
	// AttemptsWithUsage counts streams reporting usage, including reported zero.
	// Totals are partial when this is less than ProviderAttempts.
	AttemptsWithUsage int           `json:"attempts_with_usage"`
	ProviderDuration  time.Duration `json:"provider_duration"`
	CallbackDuration  time.Duration `json:"callback_duration"`
	// RequestPreparationDuration includes initial prompt rendering and per-request
	// shaping, tool enumeration, and request-history capture. It excludes compaction.
	RequestPreparationDuration time.Duration `json:"request_preparation_duration"`
	// ToolDispatchDuration sums batch wall times, not overlapping tool durations.
	// It includes synchronous dispatch observers and workspace admission waits.
	ToolDispatchDuration time.Duration `json:"tool_dispatch_duration"`
	// TimeToFirstOutput is elapsed from Run entry to first content, thinking,
	// or tool-call delta across all attempts. Nil means no meaningful output.
	TimeToFirstOutput *time.Duration `json:"time_to_first_output"`
}

type streamTiming struct {
	started                    time.Time
	duration, callbackDuration time.Duration
	firstOutput, firstAccepted *time.Duration
}

func (t *streamTiming) observe(chunk llm.CompletionChunk, accepted bool, now time.Time) {
	if accepted && t.firstAccepted == nil {
		t.firstAccepted = new(now.Sub(t.started))
	}
	if t.firstOutput == nil && (chunk.Content != "" || chunk.Thinking != "" || len(chunk.ToolCalls) != 0) {
		t.firstOutput = new(now.Sub(t.started))
	}
}
