package teasink

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// CompactionSettledMsg carries independently attributed compactor usage.
type CompactionSettledMsg struct{ runner.CompactionSettled }

// OnCompactionSettled forwards an owned usage snapshot even after cancellation.
func (s *Sink) OnCompactionSettled(_ context.Context, e runner.CompactionSettled) {
	if e.Inference.Usage != nil {
		e.Inference.Usage = new(*e.Inference.Usage)
	}
	s.flush()
	s.dispatch(CompactionSettledMsg{CompactionSettled: e})
}
