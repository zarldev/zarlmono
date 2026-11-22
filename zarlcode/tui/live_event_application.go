package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"
)

var errLiveApplicationUnknown = errors.New("live event application outcome unknown")

// applyLiveEvent is the narrow state/transcript dispatcher shared by normal
// receipts and shutdown. It never replays Update, executes commands, or admits
// UI input. Closing gates effect construction in the owning handlers.
func (m *UI) applyLiveEvent(msg tea.Msg) (tea.Cmd, bool, error) {
	if ok, cmd := m.handleRunnerMsg(msg); ok {
		return cmd, true, nil
	}
	switch value := msg.(type) {
	case manualCompactionAppliedMsg:
		return m.applyManualCompaction(value), true, nil
	case sessionPersistedMsg:
		cmd, handled := m.handleDraftPersistenceMsg(value)
		return cmd, handled, nil
	case settledLiveTurnMsg:
		return m.handleSettledLiveTurn(value), true, nil
	case foregroundSwarmActivationMsg:
		return m.handleForegroundSwarmActivation(value), true, nil
	case foregroundSwarmStoppedMsg:
		m.handleForegroundSwarmStopped(value)
		return nil, true, nil
	default:
		return nil, false, errLiveApplicationUnknown
	}
}
