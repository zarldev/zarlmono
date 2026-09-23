package tui

import (
	"errors"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// liveTurnOperation is owned by Update. Command closures copy its identity;
// runner task identity is filled only when its start event is applied.
type liveTurnOperation struct {
	sessionID      string
	generation     uint64
	turnID         string
	promptID       string
	queuedID       int
	ended          bool
	failed         bool
	completed      bool
	saving         bool
	promotingExact bool
	source         *sourceObservation // trusted BEFORE receipt retained through model execution
}

type settledLiveTurnMsg struct {
	sessionID  string
	generation uint64
	result     tea.Msg
}

// SetLiveEventSink binds the same sink supplied to the live runner. Its pump
// delivers completion markers in-band, after the committed turn's events. The
// composition root retains ownership and closes it after the UI has stopped.
func (m *UI) SetLiveEventSink(sink *teasink.Sink) { m.liveSink = sink }

func (m *UI) runLiveTurn(prompt string, attachments []llm.ContentPart) tea.Cmd {
	return m.runLiveTurnInput(prompt, attachments, 0)
}

func (m *UI) runLiveTurnInput(prompt string, attachments []llm.ContentPart, queuedID int) tea.Cmd {
	if m.liveOperation != nil {
		return m.blockUnsettledSubmit()
	}
	m.session.EnsureIdentity(uuid.NewString(), time.Now())
	m.liveGeneration++
	op := liveTurnOperation{sessionID: m.session.ID, generation: m.liveGeneration, promptID: uuid.NewString(), queuedID: queuedID}
	m.liveOperation = &op
	if m.durableDispatch() && m.unsavedTurnError == nil {
		return m.enqueueBeforeTurn(op, prompt, attachments, queuedID)
	}
	run := RunFnWithAttachments(engine.WithToolOutputSession(m.appContext(), op.sessionID), m.live, prompt, llm.CloneContentParts(attachments))
	var historyStore *db.Store
	if m.durableDispatch() && m.exactResume {
		historyStore = m.settings.Store
		live, ctx := m.live, engine.WithToolOutputSession(m.appContext(), op.sessionID)
		parts := llm.CloneContentParts(attachments)
		run = func() tea.Msg {
			if err := live.RunRecordedTurn(ctx, prompt, parts, historyStore, op.sessionID); err != nil {
				return beforeTurnFailedMsg{prompt: prompt, attachments: parts, err: err}
			}
			return liveTurnFinishedMsg{}
		}
	}
	if queuedID != 0 {
		// A prior save failure must not prevent explicit queued input from
		// running. Admission still atomically owns/removes the queued prompt.
		live, ctx := m.live, engine.WithToolOutputSession(m.appContext(), op.sessionID)
		run = func() tea.Msg {
			reservation, err := live.ReserveQueuedTurn(queuedID, prompt)
			if err != nil {
				return beforeTurnFailedMsg{prompt: prompt, err: err}
			}
			defer reservation.Release()
			if historyStore != nil {
				err = reservation.RunRecordedTurn(ctx, prompt, nil, historyStore, op.sessionID)
			} else {
				err = reservation.RunTurn(ctx, prompt, nil)
			}
			if err != nil {
				return beforeTurnFailedMsg{prompt: prompt, err: err}
			}
			return liveTurnFinishedMsg{}
		}
	}
	sink := m.liveSink
	return func() tea.Msg {
		marker := settledLiveTurnMsg{sessionID: op.sessionID, generation: op.generation, result: run()}
		if sink != nil {
			sink.AfterEvents(marker)
			return nil
		}
		// Standalone embedders without a sink retain command completion. They
		// cannot establish rewind eligibility from this fallback.
		return marker
	}
}

func (m *UI) bindStartedLiveTurn(e teasink.ConversationStartedMsg) {
	if e.Depth != 0 || m.liveOperation == nil || m.liveOperation.sessionID != m.session.ID {
		return
	}
	m.liveOperation.turnID = e.TaskID
	if m.liveOperation.queuedID != 0 {
		m.timeline.removeQueueIntent()
	}
}

func (m *UI) markEndedLiveTurn(e teasink.ConversationEndedMsg, failed bool) {
	if e.Depth == 0 && m.liveOperation != nil && m.liveOperation.turnID == e.TaskID {
		m.liveOperation.ended = true
		m.liveOperation.failed = failed
	}
}

func (m *UI) handleSettledLiveTurn(msg settledLiveTurnMsg) tea.Cmd {
	op := m.liveOperation
	if op == nil || op.sessionID != msg.sessionID || op.generation != msg.generation || m.session.ID != msg.sessionID {
		return nil
	}
	if failed, ok := msg.result.(beforeTurnFailedMsg); ok {
		if errors.Is(failed.err, db.ErrCheckpointConflict) {
			m.sourceConflict = true
		}
		if op.promotingExact && !failed.checkpointSaved {
			m.exactResume = false
		}
		m.liveOperation = nil
		if op.queuedID != 0 {
			m.session.SetErrorToast("queued turn not dispatched; input remains queued: " + failed.err.Error())
			return m.toastExpiryCmd()
		}
		return m.restoreBeforeInput(failed)
	}
	if failed, ok := msg.result.(turnSetupFailedMsg); ok {
		m.liveOperation = nil
		_, cmd := m.handleRunnerMsg(failed)
		return cmd
	}
	op.completed = true
	return m.persistSettledLiveTurn()
}

func (m *UI) persistSettledLiveTurn() tea.Cmd {
	op := m.liveOperation
	if op == nil || op.sessionID != m.session.ID || !op.completed || !op.ended || op.turnID == "" || op.saving {
		return nil
	}
	m.session.reconcileTopLevelRun()
	// A marker is an applied-event barrier, not a database barrier. Retain
	// operation ownership until this non-coalescible full save completes.
	source, sourceErr := m.observeCompletedSource()
	boundary := &completedSessionBoundary{
		sessionID: op.sessionID, generation: op.generation, turnID: op.turnID,
		watermark: m.timeline.transcriptThread().Revision(), source: source, sourceErr: sourceErr,
	}
	m.completedBoundary = boundary
	snapshot, err := m.sessionSnapshotForBoundary(m.live.ContextSnapshot(), op.turnID, boundary.watermark, rewind.InitialContinuation{})
	boundary.snapshot = snapshot
	if err != nil {
		return m.failLiveSettlement(err)
	}
	if sourceErr != nil {
		return m.failLiveSettlement(sourceErr)
	}
	op.saving = true
	return m.enqueueSessionPersist(sessionPersistOp{
		kind: sessionPersistFull, generation: m.transcriptGeneration,
		snapshot: snapshot, settledGeneration: op.generation,
		sourceObserved: source, guarded: snapshot.exact || m.durableDispatch(),
	})
}

func (m *UI) finishSettledLiveTurn(msg sessionPersistedMsg) tea.Cmd {
	op := m.liveOperation
	if op == nil || msg.settledGeneration == 0 || msg.settledGeneration != op.generation || msg.sessionID != op.sessionID {
		return nil
	}
	if msg.err != nil {
		return m.failLiveSettlement(msg.err)
	}
	if m.session.ID != op.sessionID || !op.ended {
		return nil
	}
	recoveredSave := m.unsavedTurnError != nil
	m.unsavedTurnError = nil
	m.completedBoundary = nil
	m.liveOperation = nil
	m.settledTurnID = op.turnID
	m.settledWatermark = msg.revision
	m.initialContinuation = rewind.InitialContinuation{}
	if recoveredSave {
		m.session.SetSuccessToast("turn saved; submit your prompt to continue")
		if m.hasIdleQueuedTurn() {
			m.session.SetSuccessToast("turn saved — Enter: send queued input; composer retained")
		}
		// Restored durability must not implicitly dispatch previously queued input.
		return m.toastExpiryCmd()
	}
	if op.failed {
		return nil
	}
	return m.launchQueuedTurn()
}

func (m *UI) failLiveSettlement(err error) tea.Cmd {
	// Each failed attempt is consumed here once. An earlier unsaved turn must
	// not suppress the diagnostic for a later, potentially different failure.
	slog.ErrorContext(m.appContext(), "turn settlement save", "err", err)
	m.unsavedTurnError = err
	if errors.Is(err, db.ErrCheckpointConflict) || errors.Is(err, db.ErrTranscriptConflict) {
		m.sourceConflict = true
	}
	// Runtime completion and durability are separate. Keep the last exact
	// head, but release UI turn ownership so the next prompt can run in memory.
	m.liveOperation = nil
	m.session.SetErrorToast("Not saved — " + m.sessionSaveFailureSummary() + "; you can continue in memory, but restart loses unsaved turns. Ctrl+q: recovery.")
	return nil // persistent warning; queued input remains explicitly dispatchable
}

func (m *UI) liveSettlementStatus() string {
	if m.sourceConflict {
		return sourceConflictNotice
	}
	if m.unsavedTurnError != nil {
		return "Not saved — " + m.sessionSaveFailureSummary() + ". Ctrl+q: recovery"
	}
	op := m.liveOperation
	if op == nil || !op.ended {
		if m.hasIdleQueuedTurn() {
			return "Enter: send queued input; composer retained"
		}
		return ""
	}
	if op.saving {
		return "saving completed turn; input retained"
	}
	return "waiting for completed turn to settle and save"
}

func (m *UI) blockUnsettledSubmit() tea.Cmd {
	m.session.SetErrorToast("waiting for the previous turn to settle and save")
	return m.toastExpiryCmd()
}
