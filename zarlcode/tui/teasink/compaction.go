package teasink

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// CompactionSettledMsg carries independently attributed compactor usage.
type CompactionSettledMsg struct{ runner.CompactionSettled }

// OnCompactionSettled forwards an owned inference snapshot even after cancellation.
func (s *Sink) OnCompactionSettled(_ context.Context, e runner.CompactionSettled) {
	e.Inference = e.Inference.Clone()
	s.flush()
	s.dispatch(CompactionSettledMsg{CompactionSettled: e})
}
