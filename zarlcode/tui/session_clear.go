package tui

import "github.com/zarldev/zarlmono/zarlcode/engine"

// reserveEmptySession clears context and selection hints before publishing a
// new UI identity. The caller holds the returned reservation through that
// publication, so a busy save can never turn a clear into a silent no-op.
func (m *UI) reserveEmptySession() (func(), error) {
	if m.sessionPersistRunning || len(m.sessionPersistQueue) != 0 {
		return nil, engine.ErrRuntimeBusy
	}
	if m.live == nil { // Intro-only surfaces do not own an execution runtime.
		return func() {}, nil
	}
	reservation, err := m.live.ReserveRuntime()
	if err != nil {
		return nil, err
	}
	if err := reservation.RestoreSessionContext(nil, engine.MCPActivations{}); err != nil {
		reservation.Release()
		return nil, err
	}
	return reservation.Release, nil
}
