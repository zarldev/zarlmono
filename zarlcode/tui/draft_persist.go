package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
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
	sessionPersistRename
)

type sessionPersistOp struct {
	kind              sessionPersistKind
	generation        uint64
	draft             db.SessionRecord
	transcript        *transcriptSnapshot
	snapshot          *sessionSnapshot
	oldID             string
	label             string
	done              chan sessionPersistedMsg
	claimed           *atomic.Bool // exactly one command or shutdown executor owns completion
	settledGeneration uint64
	before            *beforeTurn
	beforeGeneration  uint64
	cancel            context.CancelFunc
	sourceObserved    sourceObservation
	sourceWritten     *atomic.Pointer[sourceWrite]
	sourceErr         error
	guarded           bool // retain the supplied source observation; never prefix-retry
	retry             *sessionSaveRetry
}

type sessionPersistedMsg struct {
	kind              sessionPersistKind
	generation        uint64
	sessionID         string
	revision          uint64
	err               error
	settledGeneration uint64
	beforeGeneration  uint64
	turnResult        tea.Msg
	operation         <-chan sessionPersistedMsg // FIFO identity, not transcript/draft generation
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
		current := m.sessionPersistCurrent
		if current == nil || msg.operation != current.done {
			return nil, true // duplicate/old acknowledgement must not release a newer write
		}
		if msg.err == nil && msg.sessionID == m.session.ID {
			m.acknowledgeDurableDraft(current)
			m.acknowledgeSource(current)
		}
		if msg.err == nil && msg.sessionID == m.session.ID &&
			(msg.kind == sessionPersistTranscript || msg.kind == sessionPersistFull) {
			if m.transcriptPersistedSessionID != msg.sessionID || msg.revision > m.transcriptPersisted {
				m.transcriptPersistedSessionID = msg.sessionID
				m.transcriptPersisted = msg.revision
			}
			m.lastSessionPersistError = ""
		}
		m.sessionPersistRunning = false
		m.sessionPersistCurrent = nil
		if msg.sessionID == m.session.ID && errors.Is(msg.err, db.ErrCheckpointConflict) {
			m.sourceConflict = true
		}
		if current.retry != nil {
			cmd := m.finishSessionRetry(current, msg)
			return tea.Batch(m.startNextSessionPersist(), cmd), true
		}
		if msg.beforeGeneration != 0 {
			settled := m.handleSettledLiveTurn(settledLiveTurnMsg{
				sessionID: msg.sessionID, generation: msg.beforeGeneration, result: msg.turnResult,
			})
			return tea.Batch(m.startNextSessionPersist(), settled), true
		}
		if msg.err != nil && msg.settledGeneration != 0 && msg.sessionID == m.session.ID {
			settled := m.finishSettledLiveTurn(msg)
			return tea.Batch(m.startNextSessionPersist(), settled), true
		}
		if msg.err != nil {
			if msg.sessionID != "" && msg.sessionID != m.session.ID && msg.kind != sessionPersistDelete {
				return m.startNextSessionPersist(), true
			}
			transcriptConflict := errors.Is(msg.err, db.ErrTranscriptConflict)
			errorKey := fmt.Sprintf("%d:%s:%v", msg.kind, msg.sessionID, msg.err)
			if transcriptConflict {
				errorKey = "transcript-conflict:" + msg.sessionID
			}
			if errorKey == m.lastSessionPersistError {
				return m.startNextSessionPersist(), true
			}
			m.lastSessionPersistError = errorKey
			if transcriptConflict {
				slog.WarnContext(m.appContext(), "transcript persistence conflict", "session", msg.sessionID, "err", msg.err)
				m.session.SetToastTone("session changed elsewhere; transcript updates are not being saved", toastWarn)
				cmd := m.startNextSessionPersist()
				return tea.Batch(m.toastExpiryCmd(), cmd), true
			}
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
		settled := m.finishSettledLiveTurn(msg)
		return tea.Batch(m.startNextSessionPersist(), settled), true
	default:
		return nil, false
	}
}

func (m *UI) persistedTranscriptRevision(sessionID string) uint64 {
	if sessionID == "" || sessionID != m.transcriptPersistedSessionID {
		return 0
	}
	return m.transcriptPersisted
}

func (m *UI) resetTranscriptPersistence() {
	m.transcriptPersisted = 0
	m.transcriptPersistedSessionID = ""
	m.lastSessionPersistError = ""
	m.settledTurnID = ""
	m.settledWatermark = 0
	m.initialContinuation = rewind.InitialContinuation{}
	m.exactResume = false
	m.unsavedTurnError = nil
	m.sourceConflict = false
	m.sourceBaseline = db.SessionContentVersion{}
	m.completedBoundary = nil
	m.sessionRetry = nil
	m.durableDraftText = ""
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
	if m.actionableRewindPreview() {
		return nil // cancel reschedules; apply saves the source draft transactionally
	}
	if m.unsavedTurnError != nil || (m.durableDispatch() && m.liveOperation != nil) {
		return nil // settlement atomically replaces the protected recovery prompt with the current draft
	}
	text := m.composer.text()
	pendingJSON, err := draft.EncodeWithAttachments(text, m.attachmentParts())
	if err != nil {
		m.session.SetErrorToast("draft save: " + err.Error())
		return m.toastExpiryCmd()
	}
	if text != "" || len(m.pendingAttachments) != 0 {
		m.rejectedDraftJSON = nil
	}
	if text == "" && len(m.pendingAttachments) == 0 {
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
	if m.unsavedTurnError != nil || (m.durableDispatch() && m.liveOperation != nil) {
		return nil
	}
	m.draftGeneration++
	m.rejectedDraftJSON = nil
	if m.session.ID == "" || m.settings == nil || m.settings.Store == nil {
		return nil
	}
	return m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistClearDraft, generation: m.draftGeneration, oldID: m.session.ID})
}

func (m *UI) enqueueSessionPersist(op sessionPersistOp) tea.Cmd {
	if m.rewindRecovery != "" || m.sourceConflict {
		return nil // the committed child must be recovered before any source writes
	}
	// Every FIFO row mutation carries an incoming CAS. BEFORE publishes its
	// receipt itself; deletion retires the ID and needs no outgoing receipt.
	if m.settings != nil && m.settings.Store != nil && op.before == nil {
		if !op.guarded {
			ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
			op.sourceObserved, op.sourceErr = m.observeSource(ctx, op.sessionID())
			cancel()
		}
		if op.kind != sessionPersistDelete {
			op.sourceWritten = new(atomic.Pointer[sourceWrite])
		}
	}
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
			// Adjacent transcript snapshots may share the earlier receipt. Never
			// move one across another observer or replace its captured capability.
			if queued.kind == sessionPersistTranscript && queued.sessionID() == op.sessionID() &&
				(op.sourceWritten == nil || i == len(m.sessionPersistQueue)-1) {
				if queued.sourceWritten != nil {
					op.sourceObserved, op.sourceErr, op.sourceWritten = queued.sourceObserved, queued.sourceErr, queued.sourceWritten
				}
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
	var allEntries []db.TranscriptEntry
	if op.transcript != nil {
		update = &op.transcript.update
		allEntries = op.transcript.allEntries
	}
	if op.snapshot != nil {
		update = &op.snapshot.transcript
		allEntries = op.snapshot.allEntries
	}
	if update == nil {
		return
	}
	if allEntries == nil {
		allEntries = append([]db.TranscriptEntry(nil), update.Entries...)
	}
	rebaseTranscriptUpdate(update, allEntries, revision)
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
	case sessionPersistClearDraft, sessionPersistDelete, sessionPersistRename:
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
		switch {
		case queued.sessionID() != sessionID:
			queue = append(queue, queued)
		case queued.before != nil:
			queued.before.reservation.Release()
		case queued.retry != nil:
			queued.retry.reservation.Release()
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
	if m.sourceConflict && op.sessionID() == m.session.ID && op.before == nil {
		op.sourceErr = db.ErrCheckpointConflict
	}
	if op.kind == sessionPersistTranscript || op.kind == sessionPersistFull {
		if op.sessionID() == m.transcriptPersistedSessionID {
			op.rebaseTranscript(m.transcriptPersisted)
		}
		if op.kind == sessionPersistTranscript && op.sessionID() == m.transcriptPersistedSessionID &&
			op.transcriptRevision() <= m.transcriptPersisted {
			return m.startNextSessionPersist()
		}
	}
	op.claimed = new(atomic.Bool)
	m.sessionPersistCurrent = &op
	m.sessionPersistRunning = true
	settings := m.settings
	if op.before != nil {
		return m.beforeTurnCommand(op)
	}
	baseCtx := context.WithoutCancel(m.appContext())
	return func() tea.Msg {
		if !op.claimed.CompareAndSwap(false, true) {
			return nil // shutdown already completed this returned-but-unstarted command
		}
		ctx, cancel := context.WithTimeout(baseCtx, sessionSaveCommandTTL)
		defer cancel()
		err := executeSessionPersist(ctx, settings, &op)
		if err != nil && (op.kind == sessionPersistTranscript || op.kind == sessionPersistFull) {
			err = retrySessionTranscriptPersist(ctx, settings, &op, err)
		}
		msg := sessionPersistedMsg{
			kind: op.kind, generation: op.generation, sessionID: op.sessionID(),
			revision: op.transcriptRevision(), err: err,
			settledGeneration: op.settledGeneration,
			operation:         op.done,
		}
		op.done <- msg
		close(op.done)
		return msg
	}
}

func executeSessionPersist(ctx context.Context, settings *engine.Settings, op *sessionPersistOp) error {
	if op.sourceErr != nil {
		return op.sourceErr
	}
	if op.sourceWritten != nil {
		return executeVersionedSessionPersist(ctx, settings.Store, op)
	}
	switch op.kind {
	case sessionPersistDraft:
		return settings.Store.SaveSessionDraft(ctx, op.draft)
	case sessionPersistClearDraft:
		return settings.Store.ClearSessionDraft(ctx, op.oldID)
	case sessionPersistTranscript:
		return saveTranscriptSnapshot(ctx, settings.Store, op.transcript)
	case sessionPersistFull:
		if op.snapshot.exact || op.guarded {
			return op.snapshot.commitGuarded(ctx, settings.Store, op.sourceObserved.expected())
		}
		return saveSessionSnapshot(ctx, settings, op.snapshot)
	case sessionPersistDelete:
		if op.oldID != "" && settings != nil && settings.Store != nil {
			if err := settings.Store.DeleteSessionVersioned(ctx, op.oldID, op.sourceObserved.expected()); err != nil {
				return err
			}
		}
		return clearActiveSession(ctx, settings, op.oldID)
	default:
		return nil
	}
}

func retrySessionTranscriptPersist(ctx context.Context, settings *engine.Settings, op *sessionPersistOp, original error) error {
	if op.guarded || (op.kind == sessionPersistFull && op.snapshot.exact) || errors.Is(original, db.ErrCheckpointConflict) {
		return original // transcript equality is not permission to replace the full row
	}
	target := op.transcriptRevision()
	stored, err := settings.Store.GetSessionTranscript(ctx, op.sessionID())
	if errors.Is(err, db.ErrNotFound) {
		stored = db.SessionTranscript{}
	} else if err != nil {
		return original
	}
	if stored.Revision > target || !compatibleTranscriptPrefix(stored.Entries, op.transcriptEntries(), stored.Revision) {
		return fmt.Errorf("%w: durable transcript diverges from pending transcript: %w", db.ErrTranscriptConflict, original)
	}
	op.rebaseTranscript(stored.Revision)
	// A matching canonical revision proves only the transcript write. A full
	// settlement barrier must also commit context, usage, plan and draft.
	if stored.Revision == target && op.kind == sessionPersistTranscript {
		return nil
	}
	if err := executeSessionPersist(ctx, settings, op); err != nil {
		return original
	}
	return nil
}

func (op *sessionPersistOp) transcriptEntries() []db.TranscriptEntry {
	if op.transcript != nil {
		return op.transcript.allEntries
	}
	if op.snapshot != nil {
		return op.snapshot.allEntries
	}
	return nil
}

func compatibleTranscriptPrefix(durable, pending []db.TranscriptEntry, revision uint64) bool {
	if revision == 0 {
		return true
	}
	prefix := make(map[uint64]db.TranscriptEntry, len(pending))
	for _, entry := range pending {
		if entry.Revision <= revision {
			prefix[entry.Revision] = entry
		}
	}
	if len(prefix) != len(durable) {
		return false
	}
	for _, entry := range durable {
		pendingEntry, ok := prefix[entry.Revision]
		if !ok || pendingEntry.Sequence != entry.Sequence || pendingEntry.EntryID != entry.EntryID ||
			pendingEntry.ParentID != entry.ParentID || pendingEntry.TurnID != entry.TurnID || pendingEntry.Kind != entry.Kind ||
			string(pendingEntry.PayloadJSON) != string(entry.PayloadJSON) {
			return false
		}
	}
	return true
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
	if m.unsavedTurnError != nil {
		return nil // retain the last paired head until a valid completed save succeeds
	}
	if m.exactResume {
		// Exact heads are paired with canonical bytes in one full transaction.
		// During a turn retain the last durable head until the settlement barrier;
		// an incremental transcript write would make restart reject the envelope.
		if m.liveOperation != nil {
			return nil
		}
		return m.saveSessionCmd()
	}
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
