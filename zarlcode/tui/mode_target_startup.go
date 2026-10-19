package tui

import tea "charm.land/bubbletea/v2"

// Initial mode routing uses the startup target; advisory metadata may update
// it only while it remains current. Exact restores retain their physical target.
func (m *UI) initialModeTarget() tea.Cmd {
	if !m.startupReady {
		return nil
	}
	return m.maybeModeTarget()
}

func (m *UI) readyStartupPrompt() tea.Cmd {
	if !m.startupReady || m.startupPrompt == "" || m.live == nil || m.modeTarget.err != nil || m.modeTarget.building || m.modeTarget.deferred || m.modeTarget.prepared != nil {
		return nil
	}
	prompt, attachments := m.startupPrompt, m.startupAttachments
	m.startupPrompt, m.startupAttachments = "", nil
	m.session.SetSubmittedAttachments(m.startupAttachmentMetadata)
	m.startupAttachmentMetadata = nil
	return m.runLiveTurn(prompt, attachments)
}

// A newly published session supersedes outstanding mode-build completions.
// Restores preserve the physical target; a fresh session reapplies its route.
func (m *UI) resetSessionModeTarget(restored bool) {
	m.repointSeq++
	m.modeTarget = modeTargetLifecycle{generation: m.modeTarget.generation + 1, manualOverride: restored}
	m.scheduledModeTargetCmd = nil
}
