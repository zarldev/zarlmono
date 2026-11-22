package tui

import (
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
)

const compactionObservationLimit = 3

// CompactionDiagnostic attributes a settled auxiliary inference to its actual
// compactor target. Failed records settlement failure without retaining error
// text. Value copies borrow inference sidecars; UI.CompactionObservations returns
// owned snapshots. Reported cost is evidence, never session spend.
type CompactionDiagnostic struct {
	TaskID    string
	Depth     int
	Inference compact.Inference
	Failed    bool
}

// CompactionObservations returns independently owned snapshots of the latest
// three auxiliary settlements in session order, including manual compaction
// without an active root. This bounded, transient history is not an exhaustive
// ledger or latest-root membership list. A full session reset clears it; starting
// another root does not. Call it on the UI event-loop owner, like other snapshots.
func (m *UI) CompactionObservations() []CompactionDiagnostic {
	observations := m.session.Run.compactionObservations
	if observations == nil {
		return nil
	}
	result := make([]CompactionDiagnostic, len(observations))
	for i, observation := range observations {
		observation.Inference = observation.Inference.Clone()
		result[i] = observation
	}
	return result
}

func (s *RunState) observeCompaction(e teasink.CompactionSettledMsg) {
	if len(s.compactionObservations) == compactionObservationLimit {
		copy(s.compactionObservations, s.compactionObservations[1:])
		s.compactionObservations = s.compactionObservations[:compactionObservationLimit-1]
	}
	s.compactionObservations = append(s.compactionObservations, CompactionDiagnostic{
		TaskID: string(e.TaskID), Depth: e.Depth, Inference: e.Inference.Clone(), Failed: e.Err != nil,
	})
}
