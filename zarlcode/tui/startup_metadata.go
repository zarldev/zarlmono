package tui

import "github.com/zarldev/zarlmono/zarlcode/engine"

// Startup discovery is advisory. It cannot repoint a restored, manually changed
// or busy runtime, even when the eventual provider/model name happens to match.
func (m *UI) applyStartupMetadata(msg startupReadyMsg) {
	if m.live == nil || msg.err != nil || msg.target.Provider == nil || msg.sequence != m.repointSeq || m.modeTarget.manualOverride {
		return
	}
	current := m.live.RunTarget()
	var generation engine.TargetGeneration
	if current.Facts != nil {
		generation = current.Facts.Generation
	}
	if generation != msg.generation || current.Spec != msg.origin || m.liveOperation != nil || m.sessionPersistRunning || len(m.sessionPersistQueue) != 0 {
		return
	}
	reservation, err := m.live.ReserveTargetTurn()
	if err != nil {
		return // Never block or alter a turn for advisory discovery.
	}
	defer reservation.Release()
	if err := reservation.ApplyTarget(msg.target); err != nil {
		return
	}
	spec, window := msg.target.Spec, msg.target.Window
	if msg.modelChanged {
		m.session.SetActiveProviderSpec(spec)
		m.session.ApplyProviderCostBasis(spec)
		m.session.SetSuccessToast("saved model was unavailable; switched to " + spec.Model)
	}
	m.SetContextWindow(window)
	m.SetPressureConfig(window, m.session.Run.pressureReserve)
}
