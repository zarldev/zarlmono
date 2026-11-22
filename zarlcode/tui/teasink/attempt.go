package teasink

import (
	"context"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ProviderAttemptSettledMsg carries live all-attempt usage without booking spend.
// Session spend is folded only from ConversationEndedMsg.TotalUsage.
type ProviderAttemptSettledMsg struct {
	TaskID       string
	Depth        int
	Attempt      int
	Usage        *llm.Usage
	PricingUsage *llm.PricingUsage
	Execution    *llm.ExecutionObservation
	// RateCard is the host-selected basis frozen before invocation, not a charge.
	RateCard         *llm.RateCard
	Reason           runner.TerminalReason
	Duration         time.Duration
	CallbackDuration time.Duration
	// TimeToFirstOutput is nil if no meaningful output was observed.
	TimeToFirstOutput *time.Duration
}

// OnProviderAttemptSettled forwards owned usage and observation snapshots after buffered text.
// The provider error remains at the runner boundary, not in transcript metadata.
func (s *Sink) OnProviderAttemptSettled(ctx context.Context, e runner.ProviderAttemptSettled) {
	var usage *llm.Usage
	if e.Usage != nil {
		usage = new(*e.Usage)
	}
	var pricingUsage *llm.PricingUsage
	if e.PricingUsage != nil {
		pricingUsage = new(e.PricingUsage.Clone())
	}
	var execution *llm.ExecutionObservation
	if e.Execution != nil {
		execution = new(e.Execution.Clone())
	}
	var rateCard *llm.RateCard
	if e.RateCard != nil {
		rateCard = new(e.RateCard.Clone())
	}
	var firstOutput *time.Duration
	if e.TimeToFirstOutput != nil {
		firstOutput = new(*e.TimeToFirstOutput)
	}
	s.flush()
	s.dispatch(ProviderAttemptSettledMsg{TaskID: string(e.TaskID), Depth: e.Depth,
		Attempt: e.Attempt, Usage: usage, PricingUsage: pricingUsage, Execution: execution, RateCard: rateCard, Reason: e.Reason,
		Duration: e.Duration, CallbackDuration: e.CallbackDuration, TimeToFirstOutput: firstOutput})
}
