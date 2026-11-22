package tui

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/db"
)

// manualCompaction is owned by the UI loop. The runtime command owns its
// return channel; an application marker alone never proves command return.
type manualCompaction struct {
	sessionID       string
	generation      uint64
	cancel          context.CancelFunc
	reservation     *engine.RuntimeReservation
	recorded        bool
	claimed         atomic.Bool
	done            chan struct{}
	joined          bool
	finishing       bool
	applied         bool
	shutdownRetried bool
	result          engine.ManualCompactionResult
	err             error
	saveErr         error
	draftGeneration uint64
	source          sourceObservation
	retry           *sessionPersistOp
}

type manualCompactionAppliedMsg struct{ operation *manualCompaction }
type manualCompactionReturnedMsg struct{ operation *manualCompaction }

// CompactNow requests manual compaction through the same admission path as the
// interactive control. The returned command is owned by the embedding program.
func (m *UI) CompactNow() tea.Cmd { return m.compactNowCmd() }

func (m *UI) startManualCompaction() tea.Cmd {
	if m.live == nil {
		return func() tea.Msg { return compactNowFailedMsg{Error: "live runner is not available"} }
	}
	if m.persistenceClosing || m.manualCompaction != nil || m.liveOperation != nil || m.sessionRetry != nil || m.unsavedTurnError != nil || m.sessionPersistRunning || len(m.sessionPersistQueue) != 0 || m.sourceConflict || m.rewindRecovery != "" || m.foreground != nil || m.session.Run.Running {
		return func() tea.Msg { return compactNowFailedMsg{Error: "session is not settled"} }
	}
	if len(m.live.ContextSnapshot()) == 0 {
		return func() tea.Msg { return compactNowFinishedMsg{} }
	}
	recorded := m.settings != nil && m.settings.Store != nil
	if recorded {
		// An in-memory ID is not a durable parent. In particular, do not create a
		// session just to attach a helper invocation to it.
		if m.session.ID == "" {
			return func() tea.Msg { return compactNowFailedMsg{Error: "save a session before compacting"} }
		}
		ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
		rec, err := m.settings.Store.GetSession(ctx, m.session.ID)
		cancel()
		if err != nil || rec.Workspace != m.settings.WorkspaceRoot() {
			return func() tea.Msg { return compactNowFailedMsg{Error: "session is not durably saved"} }
		}
	}
	reservation, err := m.live.ReserveTurn()
	if err != nil {
		return func() tea.Msg { return compactNowFailedMsg{Error: err.Error()} }
	}
	ctx, cancel := context.WithCancel(m.appContext())
	m.liveGeneration++
	// No writes are pending at admission. Retain our acknowledged version now,
	// so shutdown can persist with its own context after the application stops.
	op := &manualCompaction{sessionID: m.session.ID, generation: m.liveGeneration, cancel: cancel, reservation: reservation, recorded: recorded, done: make(chan struct{}), source: sourceObservation{version: m.sourceBaseline}}
	m.manualCompaction = op
	sink := m.liveSink
	return func() tea.Msg {
		if !op.claimed.CompareAndSwap(false, true) {
			return nil
		}
		// Preserve panic propagation, but never orphan the operation's join or
		// incurred evidence. Only a normal return replaces this safe diagnostic.
		op.err = errors.New("manual compaction interrupted")
		defer close(op.done)
		defer func() {
			// Do not wait for application here: a bounded event queue may be full.
			if sink != nil {
				sink.AfterEvents(manualCompactionAppliedMsg{operation: op})
			}
		}()
		if recorded {
			op.result, op.err = reservation.CompactRecordedSession(ctx, op.sessionID)
		} else {
			op.result, op.err = reservation.CompactNow(ctx)
		}
		return manualCompactionReturnedMsg{operation: op}
	}
}

func (m *UI) applyManualCompaction(msg manualCompactionAppliedMsg) tea.Cmd {
	op := msg.operation
	if op != m.manualCompaction || op.applied || op.sessionID != m.session.ID || op.generation != m.liveGeneration {
		return nil
	}
	op.applied = true
	// A marker may be delivered before the command returns. Never finalize on
	// marker receipt alone; shutdown will join the return as well.
	if !op.joined {
		return nil
	}
	return m.finishManualCompaction(op)
}

func (m *UI) joinManualCompaction(msg manualCompactionReturnedMsg) tea.Cmd {
	op := msg.operation
	if op != m.manualCompaction {
		return nil
	}
	op.joined = true
	if m.liveSink == nil {
		op.applied = true
	}
	return m.finishManualCompaction(op)
}

func (m *UI) finishManualCompaction(op *manualCompaction) tea.Cmd {
	if op != m.manualCompaction || !op.joined || !op.applied || op.finishing {
		return nil
	}
	op.finishing = true
	if op.err != nil {
		m.session.SetErrorToast("compact: " + op.err.Error())
	} else {
		m.applyCompactNowFinished(compactNowFinishedMsg{Before: op.result.MessagesBefore, After: op.result.MessagesAfter, BytesTrimmed: op.result.BytesTrimmed, Engine: op.result.Engine})
	}
	if !op.recorded {
		m.releaseManualCompaction(op)
		return m.toastExpiryCmd()
	}
	return m.persistManualSnapshot(op)
}

func (m *UI) persistManualSnapshot(op *manualCompaction) tea.Cmd {
	// Snapshot after actual execution, including incurred evidence on failure.
	// Never use sessionSnapshot here: it can skip a revision-equal full save.
	messages := m.live.ContextSnapshot()
	snapshot, err := m.sessionSnapshotWithContext(messages)
	if err != nil {
		m.manualSaveFailure(op, err)
		return m.toastExpiryCmd()
	}
	op.saveErr = nil
	op.draftGeneration = m.draftGeneration
	persist := sessionPersistOp{kind: sessionPersistFull, snapshot: snapshot, manual: op, sourceObserved: op.source, guarded: true}
	return tea.Batch(m.enqueueSessionPersist(persist), m.toastExpiryCmd())
}

func (m *UI) releaseManualCompaction(op *manualCompaction) {
	if m.manualCompaction != op {
		return
	}
	op.cancel()
	op.reservation.Release()
	m.manualCompaction = nil
}

func (m *UI) manualSaveFailure(op *manualCompaction, err error) {
	op.saveErr = err
	m.session.SetErrorToast("compact save: " + err.Error())
	if errors.Is(err, db.ErrCheckpointConflict) {
		m.sourceConflict = true
	}
	// The runtime and its unacknowledged history remain reserved. A retry may
	// only replay the frozen storage operation; it must not invoke the helper.
}

func (m *UI) finishManualPersist(current *sessionPersistOp, msg sessionPersistedMsg) tea.Cmd {
	op := current.manual
	if op != m.manualCompaction {
		return nil
	}
	if msg.err != nil {
		frozen := *current
		frozen.done = nil
		frozen.claimed = nil
		frozen.sourceWritten = nil
		frozen.guarded = true
		op.retry = &frozen
		m.manualSaveFailure(op, fmt.Errorf("persist manual compaction: %w", msg.err))
		return nil
	}
	op.retry = nil
	op.saveErr = nil
	m.releaseManualCompaction(op)
	// Shutdown's final full save already owns the latest draft and its fresh
	// context; do not queue an ordinary write using the canceled application.
	if !m.persistenceClosing && m.draftGeneration != op.draftGeneration {
		return m.enqueueDraftPersist(m.draftGeneration)
	}
	return nil
}

// RetryManualCompactionSave retries persistence without invoking the provider.
// Failed writes reuse frozen bytes; failed snapshot capture can be retried after
// repairing the draft while the unchanged compacted context remains reserved.
func (m *UI) RetryManualCompactionSave() tea.Cmd {
	op := m.manualCompaction
	if op == nil {
		return nil
	}
	if reason := m.manualSaveRetryUnavailable(); reason != "" {
		m.session.SetErrorToast(reason)
		return nil
	}
	return m.retryManualPersistence(op)
}

func (m *UI) retryManualPersistence(op *manualCompaction) tea.Cmd {
	if op.retry == nil {
		return m.persistManualSnapshot(op)
	}
	retry := *op.retry
	op.retry = nil
	op.saveErr = nil
	retry.manual = op
	retry.done = nil
	retry.claimed = nil
	retry.sourceWritten = new(atomic.Pointer[sourceWrite])
	return m.enqueueSessionPersist(retry)
}

func (m *UI) manualSaveRetryUnavailable() string {
	op := m.manualCompaction
	switch {
	case m.sourceConflict:
		return "Saving paused: session changed elsewhere. Export before reloading."
	case m.persistenceClosing:
		return "Shutdown owns the pending manual compaction save."
	case !op.finishing || !op.joined || !op.applied || m.sessionPersistRunning || len(m.sessionPersistQueue) != 0:
		return "Wait for manual compaction and its current save to settle."
	case op.saveErr == nil:
		return "No failed manual compaction save to retry."
	default:
		return ""
	}
}

// manualShutdown joins the actual command return. A timeout retains ownership
// for the next FlushSessionPersistence call.
func (m *UI) manualShutdown() (bool, error) {
	op := m.manualCompaction
	if op == nil {
		return false, nil
	}
	op.cancel()
	if op.claimed.CompareAndSwap(false, true) {
		op.joined = true
		op.applied = true
		m.releaseManualCompaction(op)
		return true, nil
	}
	if !op.joined {
		select {
		case <-op.done:
			op.joined = true
		default:
			return false, nil
		}
	}
	if !op.applied {
		if m.liveSink == nil {
			op.applied = true
		} else {
			return false, nil
		}
	}
	if !op.finishing {
		m.finishManualCompaction(op)
		return true, nil
	}
	if op.saveErr != nil && op.shutdownRetried {
		return false, fmt.Errorf("manual compaction save remains unsaved: %w", op.saveErr)
	}
	if op.saveErr != nil && !m.sessionPersistRunning && len(m.sessionPersistQueue) == 0 {
		op.shutdownRetried = true
		m.retryManualPersistence(op)
		return true, nil
	}
	return false, nil
}

// manualWaitChannel is selected alongside receipt availability during shutdown.
func (m *UI) manualWaitChannel() <-chan struct{} {
	if op := m.manualCompaction; op != nil && !op.joined {
		return op.done
	}
	return nil
}
