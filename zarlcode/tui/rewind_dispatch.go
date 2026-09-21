package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// beforeTurn transfers a reservation from Update to the persistence FIFO.
// It excludes runtime mutations before capture, throughout the FIFO wait and
// durable commit, until atomic conversion to turn admission or release.
type beforeTurn struct {
	input          rewind.CaptureInput
	expectedActive string
	expectedSource sourceObservation
	sourceWritten  *atomic.Pointer[sourceWrite]
	attachments    []llm.ContentPart
	reservation    *engine.RuntimeReservation
	mu             sync.Mutex
	stopping       bool
	admitted       bool
}

// stop excludes future dispatch while the reserved recovery save finishes.
func (b *beforeTurn) stop() {
	b.mu.Lock()
	b.stopping = true
	b.mu.Unlock()
}

func (b *beforeTurn) admit() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopping || b.admitted {
		return false
	}
	b.admitted = true
	return true
}

func (b *beforeTurn) stopped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopping
}

func (m *UI) durableDispatch() bool {
	return m.settings != nil && m.settings.Store != nil && m.liveSink != nil
}

func (m *UI) enqueueBeforeTurn(op liveTurnOperation, prompt string, attachments []llm.ContentPart, queuedID int) tea.Cmd {
	var reservation *engine.RuntimeReservation
	var err error
	if queuedID != 0 {
		reservation, err = m.live.ReserveQueuedTurn(queuedID, prompt)
	} else {
		reservation, err = m.live.ReserveRuntime()
	}
	if err != nil {
		return m.rejectBeforeTurn(op, prompt, attachments, err)
	}
	transferred := false
	defer func() {
		if !transferred {
			reservation.Release()
			if m.liveOperation.promotingExact {
				m.exactResume = false
			}
		}
	}()
	canonical, err := m.timeline.transcriptThread().CaptureCheckpoint()
	if err != nil {
		return m.rejectBeforeTurn(op, prompt, attachments, err)
	}
	if !m.exactResume {
		if m.initialActiveErr != nil {
			return m.rejectBeforeTurn(op, prompt, attachments, m.initialActiveErr)
		}
		if canonical.Revision() != 0 {
			return m.rejectBeforeTurn(op, prompt, attachments, errors.New("exact checkpoint protection is unavailable for this legacy session; start a new conversation"))
		}
		// Suppress independent transcript writes while the atomic promotion is
		// pending. Only this operation may roll it back on a BEFORE failure.
		m.liveOperation.promotingExact = true
		m.exactResume = true
	}
	snapshot, err := m.sessionSnapshotWithContext(nil)
	if err != nil {
		return m.rejectBeforeTurn(op, prompt, attachments, err)
	}
	// Retain the submitted prompt durably until settlement commits.
	// Attachment bytes are retained with the unsubmitted draft and boundary.
	snapshot.record.PendingJSON, err = draft.EncodeWithAttachments(prompt, attachments)
	if err != nil {
		return m.rejectBeforeTurn(op, prompt, attachments, err)
	}
	ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
	defer cancel()
	sourceVersion, err := m.observeSource(ctx, op.sessionID)
	if err != nil {
		return m.rejectBeforeTurn(op, prompt, attachments, err)
	}
	before := &beforeTurn{
		input: rewind.CaptureInput{
			ID: uuid.NewString(), SessionID: op.sessionID, Workspace: m.settings.WorkspaceRoot(),
			Boundary: rewind.Boundary{PromptID: op.promptID, PromptText: prompt, HasAttachments: len(attachments) != 0,
				Attachments:   llm.CloneContentParts(attachments),
				SettledTurnID: m.settledTurnID, EventWatermark: m.settledWatermark, InitialContinuation: m.initialContinuation},
			Transcript: canonical,
		},
		expectedActive: m.initialActive,
		expectedSource: sourceVersion,
		attachments:    llm.CloneContentParts(attachments), reservation: reservation,
	}
	before.sourceWritten = new(atomic.Pointer[sourceWrite])
	m.liveOperation.source = &sourceObservation{version: sourceVersion.version,
		preceding: append(append([]*atomic.Pointer[sourceWrite](nil), sourceVersion.preceding...), before.sourceWritten)}
	transferred = true
	return m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistFull, generation: m.transcriptGeneration,
		snapshot: snapshot, before: before, beforeGeneration: op.generation, sourceWritten: before.sourceWritten})
}

func (m *UI) rejectBeforeTurn(op liveTurnOperation, prompt string, attachments []llm.ContentPart, err error) tea.Cmd {
	return func() tea.Msg {
		return settledLiveTurnMsg{sessionID: op.sessionID, generation: op.generation,
			result: beforeTurnFailedMsg{prompt: prompt, attachments: attachments, err: err}}
	}
}

type beforeTurnFailedMsg struct {
	prompt          string
	attachments     []llm.ContentPart
	err             error
	checkpointSaved bool
}

// saveBeforeTurn publishes the guarded recovery row, immutable history and BEFORE
// checkpoint in one transaction. Retained batches are acknowledged only afterward.
func saveBeforeTurn(ctx context.Context, store *db.Store, snapshot *sessionSnapshot, input rewind.CaptureInput, expectedActive string, expectedSource db.SessionContentVersion) (db.SessionContentVersion, error) {
	if initial := input.Boundary.InitialContinuation; initial != (rewind.InitialContinuation{}) {
		branch, err := store.GetSessionBranch(ctx, input.SessionID)
		if err != nil {
			return db.SessionContentVersion{}, err
		}
		if !initial.Matches(branch) {
			return db.SessionContentVersion{}, rewind.ErrInvalid
		}
	}
	checkpoint, err := rewind.PrepareHistory(input)
	if err != nil {
		return db.SessionContentVersion{}, err
	}
	version, err := store.CommitBeforeTurnVersioned(ctx, snapshot.record, snapshot.transcript, checkpoint, expectedActive, expectedSource, snapshot.historyBatches...)
	if err != nil {
		return db.SessionContentVersion{}, err
	}
	snapshot.acknowledgeHistory()
	return version, nil
}

func (b *beforeTurn) run(ctx, saveCtx context.Context, store *db.Store, snapshot *sessionSnapshot) (tea.Msg, error) {
	defer b.reservation.Release()
	if err := b.save(saveCtx, store, snapshot); err != nil {
		return nil, err
	}
	input := b.input
	if !b.admit() {
		return beforeTurnFailedMsg{prompt: input.Boundary.PromptText, attachments: b.attachments, err: context.Canceled, checkpointSaved: true}, nil
	}
	if err := b.reservation.RunRecordedTurn(engine.WithToolOutputSession(ctx, input.SessionID), input.Boundary.PromptText, b.attachments, store, input.SessionID); err != nil {
		return beforeTurnFailedMsg{prompt: input.Boundary.PromptText, attachments: b.attachments, err: err, checkpointSaved: true}, nil
	}
	return liveTurnFinishedMsg{}, nil
}

// save persists the protected input and exact head without admitting a turn.
// Shutdown uses it for a command it claimed before Bubble Tea started execution.
// The caller retains responsibility for releasing the reservation.
func (b *beforeTurn) save(ctx context.Context, store *db.Store, snapshot *sessionSnapshot) error {
	input := b.input
	var err error
	input.Context, input.Target, err = b.reservation.Snapshot()
	if err != nil {
		return err
	}
	input.Plan, err = b.reservation.PlanSnapshot()
	if err != nil {
		return err
	}
	snapshot.record.ContextJSON, err = encodeSessionJSON(input.Context, "[]")
	if snapshot.exact {
		snapshot.record.ContextJSON, err = rewind.EncodeResume(snapshot.transcript.Revision, input.Context, input.Target, input.Boundary.SettledTurnID, input.Boundary.EventWatermark, input.Boundary.InitialContinuation)
	}
	if err != nil {
		return err
	}
	snapshot.record.PlanJSON, err = encodePlanJSON(input.Plan)
	if err != nil {
		return err
	}
	snapshot.record.Provider, snapshot.record.Model = input.Target.Provider, input.Target.Model
	saveCtx, cancel := context.WithTimeout(ctx, sessionSaveCommandTTL)
	defer cancel()
	expected := b.expectedSource.expected()
	version, err := saveBeforeTurn(saveCtx, store, snapshot, input, b.expectedActive, expected)
	if err != nil {
		return fmt.Errorf("save BEFORE checkpoint: %w", err)
	}
	b.sourceWritten.Store(&sourceWrite{before: expected, after: version})
	return nil
}

// restoreBeforeInput never overwrites a draft typed while the save was pending.
func (m *UI) restoreBeforeInput(failure beforeTurnFailedMsg) tea.Cmd {
	text := m.composer.text()
	if text != failure.prompt {
		if text != "" {
			text = failure.prompt + "\n\n" + text
		} else {
			text = failure.prompt
		}
		m.composer.setText(text)
	}
	metadata := m.session.TakeSubmittedAttachments()
	for i, part := range failure.attachments {
		attachment := pendingAttachment{Part: part}
		if i < len(metadata) {
			attachment.Metadata = metadata[i]
		}
		m.pendingAttachments = append(m.pendingAttachments, attachment)
	}
	m.session.SetErrorToast("turn not dispatched: " + failure.err.Error())
	return tea.Batch(m.toastExpiryCmd(), m.scheduleDraftSave())
}

func (m *UI) addBoundPrompt(text string, attachments []attachmentMetadata) {
	id := ""
	if m.liveOperation != nil {
		id = m.liveOperation.promptID
	}
	change := m.timeline.applyTranscript(transcript.UserSubmitted{EntryID: id, Text: text, Attachments: transcriptAttachments(attachments)})
	m.timeline.appendItem(&userItem{entryID: change.PrimaryEntryID, text: text, attachments: transcriptAttachments(attachments)})
}

func (m *UI) beforeTurnCommand(op sessionPersistOp) tea.Cmd {
	ctx, cancel := context.WithCancel(m.appContext())
	// Recovery persistence has its own bounded lifetime. Cancelling dispatch
	// during shutdown must not discard the submitted recovery prompt.
	saveCtx := context.WithoutCancel(m.appContext())
	m.sessionPersistCurrent.cancel = func() { op.before.stop(); cancel() }
	store, sink := m.settings.Store, m.liveSink
	return func() tea.Msg {
		defer cancel()
		if !op.claimed.CompareAndSwap(false, true) {
			return nil // shutdown owns this command; no sink use after it returns
		}
		result, err := op.before.run(ctx, saveCtx, store, op.snapshot)
		if err != nil {
			result = beforeTurnFailedMsg{prompt: op.before.input.Boundary.PromptText, attachments: op.before.attachments, err: err}
		}
		msg := sessionPersistedMsg{kind: op.kind, generation: op.generation, sessionID: op.sessionID(),
			revision: op.transcriptRevision(), err: err, beforeGeneration: op.beforeGeneration, turnResult: result, operation: op.done}
		// Shutdown must not close dependencies while this command still uses
		// the sink. Its pump remains alive through FlushSessionPersistence.
		if !op.before.stopped() {
			sink.AfterEvents(msg)
		}
		op.done <- msg
		close(op.done)
		return nil
	}
}
