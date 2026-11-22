package tui

import "github.com/zarldev/zarlmono/zarlcode/engine"

// AuxiliaryObservations returns independently owned latest judge/planner slots
// for the current or latest completed root, including late child settlement
// during its drain. The next root or session reset/restore/fork clears them.
// Call it on the UI event-loop owner, like other UI snapshots.
func (m *UI) AuxiliaryObservations() engine.AuxiliarySnapshot {
	return m.session.Run.auxiliaryObservations.Clone()
}

func (s *RunState) observeAuxiliary(o engine.AuxiliaryObservation) {
	owned := o.Clone()
	switch owned.Kind {
	case engine.AuxiliaryKinds.JUDGE:
		s.auxiliaryObservations.Judge = &owned
	case engine.AuxiliaryKinds.SPAWNPLANNER:
		s.auxiliaryObservations.SpawnPlanner = &owned
	default:
		return
	}
	s.bumpRevision()
}
