package teasink

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// CompactionSettledMsg carries separately attributed compactor usage, including
// failure-time usage. It never updates the task model's context occupancy.
type CompactionSettledMsg struct{ runner.CompactionSettled }

// OnCompactionSettled forwards an owned usage snapshot.
func (s *Sink) OnCompactionSettled(_ context.Context, e runner.CompactionSettled) {
	if e.Inference.Usage != nil {
		e.Inference.Usage = new(*e.Inference.Usage)
	}
	s.flush()
	s.dispatch(CompactionSettledMsg{CompactionSettled: e})
}
