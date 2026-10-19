package tui

import "github.com/zarldev/zarlmono/zarlcode/engine"

// Startup discovery is advisory. It cannot repoint a restored, manually changed
// or busy runtime, even when the eventual provider/model name happens to match.
func (m *UI) applyStartupMetadata(msg startupReadyMsg) {
	if m.live == nil || msg.sequence != m.repointSeq || m.modeTarget.manualOverride {
		return
	}
	current := m.live.RunTarget()
	if current.Spec != msg.origin || m.liveOperation != nil || m.sessionPersistRunning || len(m.sessionPersistQueue) != 0 {
		return
	}
	reservation, err := m.live.ReserveTargetTurn()
	if err != nil {
		return // Never block or alter a turn for advisory discovery.
	}
	defer reservation.Release()
	provider, spec, window := current.Provider, current.Spec, current.Window
	if msg.modelChanged {
		provider, spec = msg.provider, msg.spec
	}
	if msg.window > 0 {
		window = msg.window
	}
	if err := reservation.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: spec, Window: window}); err != nil {
		return
	}
	if msg.modelChanged {
		m.session.SetActiveProviderSpec(spec)
		m.session.ApplyProviderCostBasis(spec)
		m.session.SetSuccessToast("saved model was unavailable; switched to " + spec.Model)
	}
	m.SetContextWindow(window)
	m.SetPressureConfig(window, m.session.Run.pressureReserve)
}
