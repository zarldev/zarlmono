package teasink

import (
	"context"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// AuxiliaryObservedMsg carries owned, transient judge/planner evidence. It
// retains no callback context and does not participate in task accounting.
type AuxiliaryObservedMsg struct{ Observation engine.AuxiliaryObservation }

// AuxiliaryObserved clones before flushing buffered content and dispatching
// through the ordered pump. Cancellation does not suppress settled evidence.
func (s *Sink) AuxiliaryObserved(_ context.Context, observation engine.AuxiliaryObservation) {
	owned := observation.Clone()
	s.flush()
	s.dispatch(AuxiliaryObservedMsg{Observation: owned})
}
