package runner

import (
	"context"
	"errors"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ProviderAttemptSettled reports one invoked completion stream after it drains,
// including failure and cancellation. Attempt is one-based within TaskID; it is
// independent of loop iterations and prepared-request storage generations.
// Usage is this attempt's final reported snapshot, not a sum of stream chunks.
// Nil means unreported; a non-nil zero means reported zero. Err preserves the
// provider/cancellation cause. Reason is completed, error, or cancelled.
// Sum these snapshots OR task terminal totals, never both.
type ProviderAttemptSettled struct {
	TaskID  taskscope.ID
	Depth   int
	Attempt int
	Usage   *llm.Usage
	// PricingUsage and Execution are independently owned latest supplied sidecars.
	// Nil means absent. A later nil retains the prior snapshot; a non-nil
	// snapshot replaces it completely, including unknown fields.
	PricingUsage *llm.PricingUsage
	Execution    *llm.ExecutionObservation
	// RateCard owns the optional host-selected basis frozen before dispatch.
	// It is independent of usage presence and does not imply any price.
	RateCard *llm.RateCard
	Reason   TerminalReason
	Err      error
	// Duration runs from lazy stream invocation through return, including local
	// yield handling and provider cleanup, but excluding this settlement callback.
	Duration time.Duration
	// TimeToFirstOutput measures the first content, thinking, or tool-call delta.
	// Nil means none arrived; usage, sidecars, finish metadata and native items do
	// not qualify. A non-nil zero is an immediate output.
	TimeToFirstOutput *time.Duration
	// TimeToFirstAccepted measures the first observation accepted by recovery
	// policy, which can include finish metadata. Nil means none was accepted.
	TimeToFirstAccepted *time.Duration
	// CallbackDuration is synchronous runner yield handling, including sinks.
	// Duration minus this is approximate upstream time, NOT server inference.
	CallbackDuration time.Duration
}

func (t *taskRun) settleAttempt(ctx context.Context, sr streamResult) {
	usage, err := sr.usage, sr.err
	t.timing.ProviderAttempts++
	t.timing.ProviderDuration += sr.duration
	t.timing.CallbackDuration += sr.callbackDuration
	if t.timing.TimeToFirstOutput == nil && sr.firstOutput != nil {
		t.timing.TimeToFirstOutput = new(sr.started.Sub(t.start) + *sr.firstOutput)
	}
	if usage != nil {
		t.timing.AttemptsWithUsage++
	}
	reason := TerminalCompleted
	if err != nil {
		reason = TerminalError
		if errors.Is(err, context.Canceled) || errors.Is(err, ErrCancelled) {
			reason = TerminalCancelled
		}
	}
	observation := ProviderAttemptObservation{
		Attempt: t.attempt, Usage: usage, PricingUsage: sr.pricingUsage, Execution: sr.execution, RateCard: sr.rateCard,
	}.Clone()
	t.providerObservations = append(t.providerObservations, observation)
	// The sink owns its snapshot independently of the retained task result.
	event := observation.Clone()
	t.r.sink.OnProviderAttemptSettled(ctx, ProviderAttemptSettled{
		TaskID: t.spec.ID, Depth: t.spec.Depth, Attempt: t.attempt,
		Usage: event.Usage, PricingUsage: event.PricingUsage, Execution: event.Execution, RateCard: event.RateCard,
		Reason: reason, Err: err,
		Duration: sr.duration, CallbackDuration: sr.callbackDuration,
		TimeToFirstOutput: sr.firstOutput, TimeToFirstAccepted: sr.firstAccepted,
	})
}

// OnProviderAttemptSettled forwards one attempt snapshot under the sink mutex.
func (s *SyncSink) OnProviderAttemptSettled(ctx context.Context, e ProviderAttemptSettled) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnProviderAttemptSettled(ctx, e)
}
