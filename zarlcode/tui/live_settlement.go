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
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

var errLiveTerminalMissing = errors.New("turn execution returned without a terminal event")

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
	cancelled      bool
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
	if err := m.modeTargetSubmitBlocked(); err != nil {
		m.showModeTargetBlocked(err, "")
		return nil
	}
	if !m.durableDispatch() && queuedID == 0 && m.modeTarget.prepared != nil {
		if err := m.applyPreparedModeTarget(nil); err != nil {
			m.session.SetErrorToast("mode target: " + err.Error())
			return nil
		}
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
		ctx := engine.WithToolOutputSession(m.appContext(), op.sessionID)
		reservation, reserveErr := m.modeTargetReservation(prompt, queuedID)
		run = func() tea.Msg {
			if reserveErr != nil {
				return beforeTurnFailedMsg{prompt: prompt, err: reserveErr}
			}
			defer reservation.Release()
			var err error
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
		m.liveOperation.cancelled = m.liveOperation.cancelled || e.Reason == runner.TerminalCancelled
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
	if !op.ended || op.turnID == "" {
		// The in-band marker proves execution and child cleanup returned. A
		// missing terminal is an unknown outcome, not a still-running turn or
		// a successful exact boundary. Retain content and require explicit
		// continuation rather than silently consuming queued input.
		m.session.reconcileMissingTerminal(time.Now())
		m.timeline.recoverInterrupted()
		m.timeline.addNotice(palette.Warning.On("⚠ turn outcome unknown — execution returned without a terminal event"))
		m.completedBoundary = nil
		return m.failLiveSettlement(errLiveTerminalMissing)
	}
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
	if cmd := m.deferModeTargetQueueLaunch(!recoveredSave && !op.cancelled && !op.failed); cmd != nil {
		return cmd
	}
	if recoveredSave {
		m.session.SetSuccessToast("turn saved; submit your prompt to continue")
		if m.hasIdleQueuedTurn() {
			m.session.SetSuccessToast("turn saved — Queued input ready: Enter to send")
		}
		// Restored durability must not implicitly dispatch previously queued input.
		return m.toastExpiryCmd()
	}
	if op.cancelled {
		m.session.SetToast("cancelled")
		if m.hasIdleQueuedTurn() {
			m.session.SetToast("Cancelled — Queued input ready: Enter to send")
		}
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

// settlementNotice carries persistent status severity from its state owner.
// Progress remains informational; actual save/conflict failures remain errors.
type settlementNotice struct {
	text string
	tone toastTone
}

func (m *UI) liveSettlementStatus() settlementNotice {
	if m.sourceConflict {
		return settlementNotice{sourceConflictNotice, toastError}
	}
	if m.unsavedTurnError != nil {
		return settlementNotice{"Not saved — " + m.sessionSaveFailureSummary() + ". Ctrl+q: recovery", toastError}
	}
	if err := m.modeTarget.err; err != nil {
		return settlementNotice{"mode target: " + err.Error(), toastError}
	}
	if m.modeTarget.prepared != nil {
		return settlementNotice{"mode target ready; waiting for safe runtime admission", toastInfo}
	}
	op := m.liveOperation
	if op != nil && op.cancelled && !op.completed {
		return settlementNotice{"cancelling — waiting for cleanup; your draft is safe", toastInfo}
	}
	if op == nil || !op.ended {
		if m.hasIdleQueuedTurn() {
			return settlementNotice{"Queued input ready: Enter to send", toastInfo}
		}
		return settlementNotice{}
	}
	if op.saving {
		return settlementNotice{"saving completed turn; input retained", toastInfo}
	}
	return settlementNotice{"waiting for completed turn to settle and save", toastInfo}
}

func (m *UI) blockUnsettledSubmit() tea.Cmd {
	if m.liveOperation != nil && m.liveOperation.cancelled && !m.liveOperation.completed {
		m.session.SetToast("cancelling — waiting for cleanup; your draft is safe")
	} else {
		m.session.SetToast("waiting for the previous turn to settle and save")
	}
	return m.toastExpiryCmd()
}
