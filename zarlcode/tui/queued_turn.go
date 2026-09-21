package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// launchQueuedTurn promotes one queued-but-not-yet-injected user input into the
// next ordinary conversation turn. This covers the case where the user typed
// while the model was already streaming its final answer, so the current runner
// never reached another Steerer drain point.
func (m *UI) launchQueuedTurn() tea.Cmd {
	if m == nil || m.live == nil || m.runFn == nil {
		return nil
	}
	if m.durableDispatch() {
		queue := m.live.QueueSnapshot()
		if len(queue) == 0 {
			return nil
		}
		return m.runLiveTurnInput(queue[0].Message.Content, nil, queue[0].ID)
	}
	msg, ok := m.live.PopQueuedInput()
	if !ok || msg.Role != "user" || strings.TrimSpace(msg.Content) == "" {
		return nil
	}
	m.timeline.addInjectedUser(msg.Content)
	m.session.SetSkipStartedPrompt(msg.Content)
	return m.runFn(msg.Content)
}

// An explicit submit can recover a queue left idle by failed dispatch/settlement.
// Do not consume the composer: it is distinct from already accepted queued input.
func (m *UI) hasIdleQueuedTurn() bool {
	return m.live != nil && m.durableDispatch() && m.liveOperation == nil && !m.session.Run.Running && len(m.live.QueueSnapshot()) != 0
}
