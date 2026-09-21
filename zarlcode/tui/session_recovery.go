package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/db"
)

// completedSessionBoundary retains the actual completed identity and the source
// observed before its first write attempt. Later memory-only turns replace the
// candidate but never adopt a newly observed (possibly foreign) source row.
type completedSessionBoundary struct {
	sessionID  string
	generation uint64
	turnID     string
	watermark  uint64
	snapshot   *sessionSnapshot
	source     sourceObservation
	sourceErr  error
}

// sessionSaveRetry is an Update-owned acknowledgement capability. The FIFO owns
// the reservation through acknowledgement (or shutdown join), so delayed results
// cannot call a context changed after commit durably saved.
type sessionSaveRetry struct {
	boundary    *completedSessionBoundary
	reservation *engine.RuntimeReservation
}

// sessionRecoveryDialog reads Update-owned state so save acknowledgements remain
// visible while the user considers quitting. It never owns a persistence worker.
type sessionRecoveryDialog struct {
	ui       *UI
	quitting bool
}

type actionSessionRecovery struct{}
type actionRetrySessionSave struct{}
type actionExportLiveSession struct{}

func (actionSessionRecovery) isAction()   {}
func (actionRetrySessionSave) isAction()  {}
func (actionExportLiveSession) isAction() {}

func (m *UI) openSessionRecovery(quitting bool) {
	if m.overlay.active() {
		if d, ok := m.overlay.top().(*sessionRecoveryDialog); ok {
			d.quitting = d.quitting || quitting
			return
		}
	}
	m.overlay.push(&sessionRecoveryDialog{ui: m, quitting: quitting})
}

func (d *sessionRecoveryDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "e":
		return actionExportLiveSession{}
	case "r":
		return actionRetrySessionSave{}
	case "y", "Y":
		if d.quitting {
			return actionQuit{}
		}
	case "esc", "enter", "ctrl+c":
		return actionClose{}
	}
	return actionNone{}
}

func (d *sessionRecoveryDialog) draw(scr uv.Screen, area uv.Rectangle) {
	m := d.ui
	activity := "Runtime idle."
	if m.session.Run.Running {
		activity = "A turn is running; tools may already have changed files or external systems."
	} else if m.liveOperation != nil {
		activity = "Turn dispatch or settlement is pending."
	}
	status := "No unsaved conversation changes are known."
	if m.sessionLossRisk() {
		status = "Some conversation or input is not confirmed saved. Quitting may lose it."
	}
	if m.unsavedTurnError != nil {
		status = "Not saved: " + m.sessionSaveFailureSummary() + ". You can continue in memory; restart loses unsaved turns."
	}
	lines := []string{activity, status, m.sessionSaveRetryReason(),
		"Export saves readable conversation, not an exact-resume checkpoint.",
		"Copy queued input and composer drafts separately; attachment bytes are not a recovery bundle.",
		"Rewinding conversation does not undo files, processes, or external actions.",
	}
	if m.session.Toast != "" {
		lines = append(lines, m.session.Toast)
	}
	hints := []keyHint{{"enter / esc", "stay"}, {"e", "export conversation"}}
	if m.sessionSaveRetryUnavailable() == "" {
		hints = append(hints, keyHint{"r", "retry save"})
	}
	title := "session recovery"
	if d.quitting {
		title = "unsaved work — quit?"
		hints = append(hints, keyHint{"y", "quit anyway"})
	}
	drawActionDialog(scr, area, title, "live conversation", lines, keyLegend(hints...), 100)
}

func (m *UI) sessionLossRisk() bool {
	return m.unsavedTurnError != nil || m.liveOperation != nil || m.sessionRetry != nil ||
		m.sessionPersistRunning || len(m.sessionPersistQueue) != 0 ||
		m.session.Run.Running || m.sourceConflict || m.rewindRecovery != "" ||
		m.composer.text() != m.durableDraftText || len(m.pendingAttachments) != 0 ||
		m.startupPrompt != "" || (m.live != nil && m.live.QueueLen() != 0) ||
		m.timeline.transcriptThread().Revision() > m.persistedTranscriptRevision(m.session.ID)
}

// sessionSaveFailureSummary exposes only semantic categories, never backend
// error text that could contain private context, SQL values or credentials.
func (m *UI) sessionSaveFailureSummary() string {
	switch {
	case errors.Is(m.unsavedTurnError, db.ErrCheckpointConflict), errors.Is(m.unsavedTurnError, db.ErrTranscriptConflict):
		return "session changed elsewhere"
	case errors.Is(m.unsavedTurnError, rewind.ErrInvalid):
		return "checkpoint rejected"
	case errors.Is(m.unsavedTurnError, rewind.ErrTarget):
		return "saved target unavailable"
	case errors.Is(m.unsavedTurnError, context.DeadlineExceeded):
		return "save timed out"
	case errors.Is(m.unsavedTurnError, context.Canceled):
		return "save cancelled"
	default:
		return "save error (see log)"
	}
}

func (m *UI) sessionSaveRetryReason() string {
	if reason := m.sessionSaveRetryUnavailable(); reason != "" {
		return reason
	}
	return "Retry save writes the completed conversation without invoking the model or tools."
}

// An empty reason permits capture; runtime admission still decides atomically
// whether background activity or a concurrent caller prevents the reservation.
func (m *UI) sessionSaveRetryUnavailable() string {
	switch {
	case m.sourceConflict || errors.Is(m.unsavedTurnError, db.ErrCheckpointConflict) || errors.Is(m.unsavedTurnError, db.ErrTranscriptConflict):
		return "Saving paused: session changed elsewhere. Export/copy before explicitly reloading."
	case m.rewindRecovery != "":
		return "Continuation is saved; restart is required to activate it."
	case m.sessionRetry != nil:
		return "Retry save in progress; no model or tools are being invoked."
	case m.liveOperation != nil || m.session.Run.Running || m.sessionPersistRunning || len(m.sessionPersistQueue) != 0:
		return "Retry unavailable until the current turn and writes settle."
	case m.unsavedTurnError == nil:
		return "No failed completed-turn save to retry."
	case errors.Is(m.unsavedTurnError, rewind.ErrInvalid), errors.Is(m.unsavedTurnError, rewind.ErrTarget):
		return "Exact context is unsupported or invalid. Retrying unchanged context cannot help; export instead."
	case m.completedBoundary == nil || m.completedBoundary.sessionID != m.session.ID || m.completedBoundary.snapshot == nil || !m.completedBoundary.snapshot.exact:
		return "No validated completed boundary is available for retry; export instead."
	case m.completedBoundary.sourceErr != nil:
		return "No trusted source version is available; export before reloading."
	case m.live != nil && m.live.QueueLen() != 0:
		return "Retry unavailable while input is queued; export/copy it separately."
	default:
		return ""
	}
}

func (m *UI) retrySessionSave() tea.Cmd {
	b := m.completedBoundary
	if reason := m.sessionSaveRetryUnavailable(); reason != "" {
		m.session.SetErrorToast(reason)
		return nil
	}
	reservation, err := m.live.ReserveRuntime()
	if err != nil {
		m.session.SetErrorToast("Retry unavailable: runtime, queued input, or background work is not idle. Export remains available.")
		return nil
	}
	// Capture current context under exclusive admission, not the historical
	// candidate: compaction/target changes must be validated anew.
	messages, _, err := reservation.Snapshot()
	if err != nil {
		reservation.Release()
		m.session.SetErrorToast("Exact context is unsupported or invalid; export instead.")
		return nil
	}
	snapshot, err := m.sessionSnapshotForBoundary(messages, b.turnID, b.watermark, rewind.InitialContinuation{})
	if err != nil {
		reservation.Release()
		m.session.SetErrorToast("No validated completed boundary is available; export instead.")
		return nil
	}
	retry := &sessionSaveRetry{boundary: b, reservation: reservation}
	m.sessionRetry = retry
	return m.enqueueSessionPersist(sessionPersistOp{
		kind: sessionPersistFull, generation: m.transcriptGeneration, snapshot: snapshot,
		retry: retry, sourceObserved: b.source, guarded: true,
	})
}

func (m *UI) finishSessionRetry(op *sessionPersistOp, msg sessionPersistedMsg) tea.Cmd {
	retry := op.retry
	retry.reservation.Release()
	if m.sessionRetry != retry {
		return nil
	}
	m.sessionRetry = nil
	b := retry.boundary
	if m.completedBoundary != b || m.session.ID != b.sessionID {
		return nil
	}
	if msg.err != nil {
		m.unsavedTurnError = msg.err
		m.session.SetErrorToast(m.sessionSaveRetryReason())
		return nil
	}
	m.unsavedTurnError = nil
	m.completedBoundary = nil
	m.settledTurnID, m.settledWatermark = b.turnID, b.watermark
	m.initialContinuation = rewind.InitialContinuation{}
	m.session.SetSuccessToast("Completed conversation saved; no model or tools invoked. Composer retained.")
	// A draft edited after capture remains dirty, and no queued prompt is sent.
	if m.composer.text() != m.durableDraftText {
		return m.scheduleDraftSave()
	}
	return nil
}

func (m *UI) observeCompletedSource() (sourceObservation, error) {
	if !m.durableDispatch() {
		return sourceObservation{}, nil
	}
	if b := m.completedBoundary; b != nil && b.sessionID == m.session.ID {
		return b.source, b.sourceErr
	}
	if op := m.liveOperation; op != nil && op.source != nil {
		return m.withPendingSource(*op.source, m.session.ID), nil
	}
	ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
	defer cancel()
	return m.observeSource(ctx, m.session.ID)
}

// Record exactly the draft included in this attributable write, never the
// composer value at acknowledgement time: later edits must remain dirty.
func (m *UI) acknowledgeDurableDraft(op *sessionPersistOp) {
	var pending []byte
	switch op.kind {
	case sessionPersistClearDraft:
		m.durableDraftText = ""
		return
	case sessionPersistDraft:
		pending = op.draft.PendingJSON
	case sessionPersistFull:
		pending = op.snapshot.record.PendingJSON
	default:
		return
	}
	if text, err := draft.Decode(pending); err == nil {
		m.durableDraftText = text
	}
}
