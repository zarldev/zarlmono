package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zkit/db"
)

const draftSaveDebounce = 500 * time.Millisecond

type draftDebounceMsg struct{ Generation uint64 }

type sessionPersistKind uint8

const (
	sessionPersistDraft sessionPersistKind = iota
	sessionPersistClearDraft
	sessionPersistFull
	sessionPersistDelete
)

type sessionPersistOp struct {
	kind       sessionPersistKind
	generation uint64
	draft      db.SessionRecord
	snapshot   *sessionSnapshot
	oldID      string
}

type sessionPersistedMsg struct {
	kind       sessionPersistKind
	generation uint64
	err        error
}

func (m *UI) scheduleDraftSave() tea.Cmd {
	if m.settings == nil || m.settings.Store == nil || m.intro != nil {
		return nil
	}
	m.draftGeneration++
	generation := m.draftGeneration
	return oneShotTimerCmd(draftSaveDebounce, func(time.Time) tea.Msg {
		return draftDebounceMsg{Generation: generation}
	})
}

func (m *UI) handleDraftPersistenceMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case draftDebounceMsg:
		if msg.Generation != m.draftGeneration {
			return nil, true
		}
		return m.enqueueDraftPersist(msg.Generation), true
	case sessionPersistedMsg:
		m.sessionPersistRunning = false
		m.sessionPersistCurrent = nil
		if m.sessionPersistDone != nil {
			m.sessionPersistDone <- msg
		}
		if msg.err != nil {
			label := "session save"
			switch msg.kind {
			case sessionPersistClearDraft:
				label = "draft clear"
			case sessionPersistDraft:
				label = "draft save"
			case sessionPersistDelete:
				label = "clear"
			case sessionPersistFull:
			}
			m.session.SetErrorToast(label + ": " + msg.err.Error())
			cmd := m.startNextSessionPersist()
			return tea.Batch(m.toastExpiryCmd(), cmd), true
		}
		return m.startNextSessionPersist(), true
	default:
		return nil, false
	}
}

func (m *UI) handleComposerInputMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		before := m.composer.text()
		cmd := m.handleKey(msg)
		m.recomputeLayout()
		if m.draftScheduleSuppressed {
			m.draftScheduleSuppressed = false
			return cmd, true
		}
		if before != m.composer.text() {
			cmd = tea.Batch(cmd, m.scheduleDraftSave())
		}
		return cmd, true
	case tea.PasteMsg:
		before := m.composer.text()
		m.handlePaste(msg.Content)
		m.recomputeLayout()
		if before != m.composer.text() {
			return m.scheduleDraftSave(), true
		}
		return nil, true
	case tea.ClipboardMsg:
		before := m.composer.text()
		m.handlePaste(msg.Content)
		m.recomputeLayout()
		if before != m.composer.text() {
			return m.scheduleDraftSave(), true
		}
		return nil, true
	default:
		return nil, false
	}
}

func (m *UI) enqueueDraftPersist(generation uint64) tea.Cmd {
	text := m.composer.text()
	pendingJSON, err := draft.Encode(text)
	if err != nil {
		m.session.SetErrorToast("draft save: " + err.Error())
		return m.toastExpiryCmd()
	}
	if text == "" {
		if m.session.ID == "" {
			return nil
		}
		return m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistClearDraft, generation: generation, oldID: m.session.ID})
	}
	m.session.EnsureIdentity(uuid.NewString(), time.Now())
	record := db.SessionRecord{
		ID:          m.session.ID,
		Workspace:   m.settings.WorkspaceRoot(),
		Label:       m.session.Label,
		LabelManual: m.session.LabelManual,
		AgentName:   m.session.LastAgentName,
		Provider:    m.session.Provider,
		Model:       m.session.Model,
		PendingJSON: pendingJSON,
		CreatedAt:   m.session.CreatedAt,
	}
	return m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistDraft, generation: generation, draft: record})
}

func (m *UI) clearDraftCmd() tea.Cmd {
	m.draftGeneration++
	if m.session.ID == "" || m.settings == nil || m.settings.Store == nil {
		return nil
	}
	return m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistClearDraft, generation: m.draftGeneration, oldID: m.session.ID})
}

func (m *UI) enqueueSessionPersist(op sessionPersistOp) tea.Cmd {
	m.sessionPersistQueue = append(m.sessionPersistQueue, op)
	return m.startNextSessionPersist()
}

func (m *UI) startNextSessionPersist() tea.Cmd {
	if m.sessionPersistRunning || len(m.sessionPersistQueue) == 0 {
		return nil
	}
	op := m.sessionPersistQueue[0]
	m.sessionPersistQueue = m.sessionPersistQueue[1:]
	m.sessionPersistCurrent = &op
	m.sessionPersistRunning = true
	settings := m.settings
	baseCtx := context.WithoutCancel(m.appContext())
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(baseCtx, sessionSaveCommandTTL)
		defer cancel()
		var err error
		switch op.kind {
		case sessionPersistDraft:
			err = settings.Store.SaveSessionDraft(ctx, op.draft)
		case sessionPersistClearDraft:
			err = settings.Store.ClearSessionDraft(ctx, op.oldID)
		case sessionPersistFull:
			err = saveSessionSnapshot(ctx, settings, op.snapshot)
		case sessionPersistDelete:
			err = clearPersistedSession(ctx, settings, op.oldID)
		}
		return sessionPersistedMsg{kind: op.kind, generation: op.generation, err: err}
	}
}
