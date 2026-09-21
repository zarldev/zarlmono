package teasink

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ProviderAttemptSettledMsg carries live all-attempt usage without booking spend.
// Session spend is folded only from ConversationEndedMsg.TotalUsage.
type ProviderAttemptSettledMsg struct {
	TaskID  string
	Depth   int
	Attempt int
	Usage   *llm.Usage
	Reason  runner.TerminalReason
}

// OnProviderAttemptSettled forwards an owned usage snapshot after buffered text.
// The provider error remains at the runner boundary, not in transcript metadata.
func (s *Sink) OnProviderAttemptSettled(ctx context.Context, e runner.ProviderAttemptSettled) {
	s.flush()
	var usage *llm.Usage
	if e.Usage != nil {
		usage = new(*e.Usage)
	}
	s.dispatch(ProviderAttemptSettledMsg{TaskID: string(e.TaskID), Depth: e.Depth,
		Attempt: e.Attempt, Usage: usage, Reason: e.Reason})
}
