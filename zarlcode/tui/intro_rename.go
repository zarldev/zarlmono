package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

type introSessionRenamedMsg struct {
	ID    string
	Label string
}

type introSessionDeletedMsg struct{ ID string }

type introSessionDeleteFailedMsg struct{ Error string }

type introSessionPinnedMsg struct {
	ID       string
	Pinned   bool
	PinnedAt time.Time
}

type introSessionPinFailedMsg struct{ Error string }

type introSessionRenameFailedMsg struct{ Error string }

func (m *UI) renameIntroSession(id, label string) tea.Cmd {
	label = normalizeSessionLabel(label)
	if m.settings == nil || m.settings.Store == nil {
		return func() tea.Msg { return introSessionRenameFailedMsg{Error: "session store unavailable"} }
	}
	store := m.settings.Store
	ctx := m.appContext()
	return func() tea.Msg {
		if err := store.RenameSession(ctx, id, label); err != nil {
			return introSessionRenameFailedMsg{Error: err.Error()}
		}
		return introSessionRenamedMsg{ID: id, Label: label}
	}
}

func (m *UI) deleteIntroSession(id string) tea.Cmd {
	if m.settings == nil || m.settings.Store == nil {
		return func() tea.Msg { return introSessionDeleteFailedMsg{Error: "session store unavailable"} }
	}
	store := m.settings.Store
	ctx := m.appContext()
	return func() tea.Msg {
		if err := store.DeleteSession(ctx, id); err != nil {
			return introSessionDeleteFailedMsg{Error: err.Error()}
		}
		return introSessionDeletedMsg{ID: id}
	}
}

func (m *UI) setIntroSessionPinned(id string, pinned bool) tea.Cmd {
	if m.settings == nil || m.settings.Store == nil {
		return func() tea.Msg { return introSessionPinFailedMsg{Error: "session store unavailable"} }
	}
	pinnedAt := time.Time{}
	if pinned {
		pinnedAt = time.Now()
	}
	store := m.settings.Store
	ctx := m.appContext()
	return func() tea.Msg {
		if err := store.SetSessionPinned(ctx, id, pinned, pinnedAt); err != nil {
			return introSessionPinFailedMsg{Error: err.Error()}
		}
		return introSessionPinnedMsg{ID: id, Pinned: pinned, PinnedAt: pinnedAt}
	}
}

func (m *UI) applyIntroSessionRenamed(msg introSessionRenamedMsg) {
	if m.intro == nil {
		return
	}
	for index := range m.intro.sessions {
		if m.intro.sessions[index].ID == msg.ID {
			m.intro.sessions[index].Label = msg.Label
			m.intro.sessions[index].LabelManual = true
			break
		}
	}
	m.intro.err = ""
	m.intro.refreshMatches(string(m.intro.searchQuery))
}

func (m *UI) applyIntroSessionDeleted(msg introSessionDeletedMsg) {
	if m.intro == nil {
		return
	}
	for index := range m.intro.sessions {
		if m.intro.sessions[index].ID != msg.ID {
			continue
		}
		m.intro.sessions = append(m.intro.sessions[:index], m.intro.sessions[index+1:]...)
		if len(m.intro.sessions) == 0 {
			m.intro.cursor = 0
			m.intro.focus = introFocusPrompt
		} else if m.intro.cursor >= len(m.intro.sessions) {
			m.intro.cursor = len(m.intro.sessions) - 1
		}
		break
	}
	m.intro.err = ""
	m.intro.refreshMatches(string(m.intro.searchQuery))
}

func (m *UI) applyIntroSessionPinned(msg introSessionPinnedMsg) {
	if m.intro == nil {
		return
	}
	for index := range m.intro.sessions {
		if m.intro.sessions[index].ID != msg.ID {
			continue
		}
		m.intro.sessions[index].Pinned = msg.Pinned
		m.intro.sessions[index].PinnedAt = msg.PinnedAt
		break
	}
	m.intro.sortSessions()
	m.intro.err = ""
}
