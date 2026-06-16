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
	msg, ok := m.live.PopQueuedInput()
	if !ok || msg.Role != "user" || strings.TrimSpace(msg.Content) == "" {
		return nil
	}
	m.timeline.addInjectedUser(msg.Content)
	m.session.SetSkipStartedPrompt(msg.Content)
	return m.runFn(msg.Content)
}
