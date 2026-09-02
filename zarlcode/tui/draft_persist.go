package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/db"
)

const (
	draftSaveDebounce      = 500 * time.Millisecond
	transcriptSaveDebounce = 200 * time.Millisecond
)

type draftDebounceMsg struct{ Generation uint64 }
type transcriptDebounceMsg struct{ Generation uint64 }

type sessionPersistKind uint8

const (
	sessionPersistDraft sessionPersistKind = iota
	sessionPersistClearDraft
	sessionPersistTranscript
	sessionPersistFull
	sessionPersistDelete
)

type sessionPersistOp struct {
	kind       sessionPersistKind
	generation uint64
	draft      db.SessionRecord
	transcript *transcriptSnapshot
	snapshot   *sessionSnapshot
	oldID      string
	done       chan sessionPersistedMsg
}

type sessionPersistedMsg struct {
	kind       sessionPersistKind
	generation uint64
	revision   uint64
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
	case transcriptDebounceMsg:
		if msg.Generation != m.transcriptGeneration {
			return nil, true
		}
		return m.enqueueTranscriptPersist(), true
	case draftDebounceMsg:
		if msg.Generation != m.draftGeneration {
			return nil, true
		}
		return m.enqueueDraftPersist(msg.Generation), true
	case sessionPersistedMsg:
		if msg.err == nil && (msg.kind == sessionPersistTranscript || msg.kind == sessionPersistFull) && msg.revision > m.transcriptPersisted {
			m.transcriptPersisted = msg.revision
		}
		m.sessionPersistRunning = false
		m.sessionPersistCurrent = nil
		if msg.err != nil {
			label := "session save"
			switch msg.kind {
			case sessionPersistClearDraft:
				label = "draft clear"
			case sessionPersistDraft:
				label = "draft save"
			case sessionPersistTranscript:
				label = "transcript save"
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
	if op.done == nil {
		op.done = make(chan sessionPersistedMsg, 1)
	}
	switch op.kind {
	case sessionPersistTranscript:
		if current := m.sessionPersistCurrent; current != nil && current.sessionID() == op.sessionID() &&
			(current.kind == sessionPersistFull || current.kind == sessionPersistDelete) && op.generation <= current.generation {
			return m.startNextSessionPersist()
		}
		for i := len(m.sessionPersistQueue) - 1; i >= 0; i-- {
			queued := m.sessionPersistQueue[i]
			if (queued.kind == sessionPersistFull || queued.kind == sessionPersistDelete) && queued.sessionID() == op.sessionID() {
				if op.generation <= queued.generation {
					return m.startNextSessionPersist()
				}
				break
			}
			if queued.kind == sessionPersistTranscript && queued.sessionID() == op.sessionID() {
				m.sessionPersistQueue[i] = op
				return m.startNextSessionPersist()
			}
		}
	case sessionPersistFull:
		m.dropQueuedTranscript(op.sessionID())
	case sessionPersistDelete:
		m.dropQueuedSession(op.sessionID())
	}
	m.sessionPersistQueue = append(m.sessionPersistQueue, op)
	return m.startNextSessionPersist()
}

func (op *sessionPersistOp) rebaseTranscript(revision uint64) {
	var update *db.TranscriptUpdate
	if op.transcript != nil {
		update = &op.transcript.update
	}
	if op.snapshot != nil {
		update = &op.snapshot.transcript
	}
	if update == nil {
		return
	}
	update.ExpectedRevision = revision
	entries := update.Entries[:0]
	for _, entry := range update.Entries {
		if entry.Revision > revision {
			entries = append(entries, entry)
		}
	}
	update.Entries = entries
}

func (op sessionPersistOp) transcriptRevision() uint64 {
	if op.transcript != nil {
		return op.transcript.update.Revision
	}
	if op.snapshot != nil {
		return op.snapshot.transcript.Revision
	}
	return 0
}

func (op sessionPersistOp) sessionID() string {
	switch op.kind {
	case sessionPersistDraft:
		return op.draft.ID
	case sessionPersistClearDraft, sessionPersistDelete:
		return op.oldID
	case sessionPersistTranscript:
		if op.transcript != nil {
			return op.transcript.update.SessionID
		}
	case sessionPersistFull:
		if op.snapshot != nil {
			return op.snapshot.record.ID
		}
	}
	return ""
}

func (m *UI) dropQueuedTranscript(sessionID string) {
	queue := m.sessionPersistQueue[:0]
	for _, queued := range m.sessionPersistQueue {
		if queued.kind != sessionPersistTranscript || queued.sessionID() != sessionID {
			queue = append(queue, queued)
		}
	}
	m.sessionPersistQueue = queue
}

func (m *UI) dropQueuedSession(sessionID string) {
	queue := m.sessionPersistQueue[:0]
	for _, queued := range m.sessionPersistQueue {
		if queued.sessionID() != sessionID {
			queue = append(queue, queued)
		}
	}
	m.sessionPersistQueue = queue
}

func (m *UI) startNextSessionPersist() tea.Cmd {
	if m.sessionPersistRunning || len(m.sessionPersistQueue) == 0 {
		return nil
	}
	op := m.sessionPersistQueue[0]
	m.sessionPersistQueue = m.sessionPersistQueue[1:]
	if op.kind == sessionPersistTranscript || op.kind == sessionPersistFull {
		op.rebaseTranscript(m.transcriptPersisted)
		if op.transcriptRevision() <= m.transcriptPersisted {
			return m.startNextSessionPersist()
		}
	}
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
		case sessionPersistTranscript:
			err = saveTranscriptSnapshot(ctx, settings.Store, op.transcript)
		case sessionPersistFull:
			err = saveSessionSnapshot(ctx, settings, op.snapshot)
		case sessionPersistDelete:
			err = clearPersistedSession(ctx, settings, op.oldID)
		}
		msg := sessionPersistedMsg{kind: op.kind, generation: op.generation, revision: op.transcriptRevision(), err: err}
		op.done <- msg
		close(op.done)
		return msg
	}
}

func (m *UI) scheduleTranscriptPersist() tea.Cmd {
	if m.settings == nil || m.settings.Store == nil || m.session.ID == "" || m.timeline.transcriptThread().IsEmpty() {
		return nil
	}
	m.transcriptGeneration++
	generation := m.transcriptGeneration
	return oneShotTimerCmd(transcriptSaveDebounce, func(time.Time) tea.Msg {
		return transcriptDebounceMsg{Generation: generation}
	})
}

func (m *UI) persistTranscriptNow() tea.Cmd {
	m.transcriptGeneration++
	return m.enqueueTranscriptPersist()
}

func (m *UI) enqueueTranscriptPersist() tea.Cmd {
	snapshot, err := m.transcriptSnapshot()
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		m.session.SetErrorToast("transcript save: " + err.Error())
		return m.toastExpiryCmd()
	}
	if snapshot == nil {
		return nil
	}
	return m.enqueueSessionPersist(sessionPersistOp{
		kind:       sessionPersistTranscript,
		generation: m.transcriptGeneration,
		transcript: snapshot,
	})
}

func (m *UI) transcriptPersistenceCmd() tea.Cmd {
	switch m.timeline.takeTranscriptPersistence() {
	case transcript.Persistences.PERSISTENCEIMMEDIATE:
		return m.persistTranscriptNow()
	case transcript.Persistences.PERSISTENCEDEBOUNCED:
		return m.scheduleTranscriptPersist()
	default:
		return nil
	}
}
