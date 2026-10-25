package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
)

// foregroundSwarm owns one in-process foreground goal whose single member is the
// current LiveRunner, loaned exclusively while the goal is open. It is lifecycle
// state only: activation execution is driven through the shared settlement FIFO.
type foregroundSwarm struct {
	goal       *engine.Swarm
	cancel     context.CancelFunc
	done       chan struct{}
	generation uint64
	stopping   bool
}

// errSwarmShutdown reports a recorded swarm activation whose receipt was not
// durably settled before the application began shutdown.
var errSwarmShutdown = errors.New("swarm activation unsettled at shutdown")

// foregroundSwarmActivationMsg carries one recorded activation from the swarm
// worker to the UI event loop. The handler borrows handle.Reservation, runs the
// activation through the BEFORE/full-save FIFO, and resolves settle exactly once.
type foregroundSwarmActivationMsg struct {
	generation uint64
	handle     *engine.RecordedSwarmActivation
	settle     chan error
}

// foregroundSwarmStoppedMsg reports that a foreground goal has drained.
type foregroundSwarmStoppedMsg struct {
	generation uint64
}

func (m *UI) isSwarmOpen() bool {
	return m.foreground != nil && !m.foreground.stopping
}

func (m *UI) foregroundSwarmAdmissionError() error {
	switch {
	case m.live == nil:
		return errors.New("session not ready — run a turn first")
	case !m.durableDispatch():
		return errors.New("swarm requires durable session persistence")
	case !m.exactResume:
		return errors.New("swarm requires an exact-replay session; run a turn first")
	case m.liveOperation != nil:
		return errors.New("wait for the current turn to settle before starting a swarm")
	case m.session.Run.Running:
		return errors.New("wait for the current turn to finish before starting a swarm")
	case m.sessionRetry != nil, m.pendingTarget != nil, m.modeTarget.building, m.modeTarget.prepared != nil:
		return errors.New("wait for the pending target change to settle")
	case m.unsavedTurnError != nil, m.sourceConflict:
		return errors.New("save the pending session before starting a swarm")
	case m.hasIdleQueuedTurn():
		return errors.New("dispatch the queued input before starting a swarm")
	default:
		return nil
	}
}

// StartSwarm starts a one-member foreground goal. It is the controller entry
// shared by the terminal /swarm command and the workbench swarm-start action.
func (m *UI) StartSwarm(assignment string) tea.Cmd { return m.startForegroundSwarm(assignment) }

// StopSwarm stops and joins the current foreground goal asynchronously.
func (m *UI) StopSwarm() tea.Cmd { return m.stopForegroundSwarm() }

func (m *UI) startForegroundSwarm(assignment string) tea.Cmd {
	assignment = strings.TrimSpace(assignment)
	if assignment == "" {
		m.session.SetErrorToast("swarm assignment is required")
		return m.toastExpiryCmd()
	}
	if m.foreground != nil {
		m.session.SetErrorToast("a swarm goal is already open; stop it first")
		return m.toastExpiryCmd()
	}
	if err := m.foregroundSwarmAdmissionError(); err != nil {
		m.session.SetErrorToast(err.Error())
		return m.toastExpiryCmd()
	}

	limits := swarm.DefaultLimits()
	limits.Workers = 1
	goal := engine.NewSwarm(assignment, swarm.WithLimits(limits))
	owner := &foregroundSwarm{goal: goal, generation: m.liveGeneration + 1, done: make(chan struct{})}

	if _, err := goal.AddRecorded("foreground", m.live, assignment, func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		return owner.execute(ctx, a, m)
	}); err != nil {
		_ = goal.Close(context.WithoutCancel(m.appContext()))
		m.session.SetErrorToast("swarm start: " + err.Error())
		return m.toastExpiryCmd()
	}

	runCtx, cancel := context.WithCancel(m.appContext())
	owner.cancel = cancel
	m.foreground = owner

	go func() {
		defer close(owner.done)
		_ = goal.Run(runCtx)
	}()

	m.session.SetSuccessToast("swarm started — " + assignment)
	return m.toastExpiryCmd()
}

func (o *foregroundSwarm) execute(ctx context.Context, a *engine.RecordedSwarmActivation, m *UI) error {
	if m.liveSink == nil {
		return errors.New("swarm activation requires a live event sink")
	}
	settle := make(chan error, 1)
	m.liveSink.AfterEvents(foregroundSwarmActivationMsg{generation: o.generation, handle: a, settle: settle})
	select {
	case err := <-settle:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *UI) stopForegroundSwarm() tea.Cmd {
	owner := m.foreground
	if owner == nil || owner.stopping {
		return nil
	}
	owner.stopping = true
	go func() {
		_ = owner.goal.Close(context.WithoutCancel(m.appContext()))
		<-owner.done
		if m.liveSink != nil {
			m.liveSink.AfterEvents(foregroundSwarmStoppedMsg{generation: owner.generation})
		}
	}()
	m.session.SetToast("swarm stopping — let the current save settle")
	return m.toastExpiryCmd()
}

func (m *UI) handleForegroundSwarmStopped(msg foregroundSwarmStoppedMsg) {
	if m.foreground != nil && m.foreground.generation == msg.generation {
		m.foreground = nil
		m.session.SetSuccessToast("swarm stopped")
	}
}

// resolveSwarmSettle releases a recorded activation's callback exactly once.
func (m *UI) resolveSwarmSettle(op *liveTurnOperation, err error) {
	if op == nil || op.swarmSettle == nil {
		return
	}
	op.swarmSettle <- err
	op.swarmSettle = nil
}

// handleForegroundSwarmActivation turns one recorded activation into the shared
// live-turn operation and enqueues its BEFORE/full-save through the FIFO. The
// engine already holds handle.Reservation; this path must not reserve again.
func (m *UI) handleForegroundSwarmActivation(msg foregroundSwarmActivationMsg) tea.Cmd {
	if m.foreground == nil || m.foreground.generation != msg.generation {
		msg.settle <- errors.New("stale swarm activation")
		return nil
	}
	if m.liveOperation != nil {
		msg.settle <- ErrWorkbenchBusy
		return nil
	}
	if msg.handle == nil || msg.handle.Reservation == nil {
		msg.settle <- errors.New("swarm activation missing reservation")
		return nil
	}

	m.liveGeneration++
	op := liveTurnOperation{
		sessionID:   m.session.ID,
		generation:  m.liveGeneration,
		promptID:    uuid.NewString(),
		swarmSettle: msg.settle,
	}
	m.liveOperation = &op
	return m.enqueueSwarmBeforeTurn(op, msg.handle)
}

func (m *UI) enqueueSwarmBeforeTurn(op liveTurnOperation, handle *engine.RecordedSwarmActivation) tea.Cmd {
	reservation := handle.Reservation
	canonical, err := m.timeline.transcriptThread().CaptureCheckpoint()
	if err != nil {
		return m.rejectSwarmBeforeTurn(op, handle, err)
	}
	if _, _, exact, err := reservation.PersistenceSnapshot(); err != nil {
		return m.rejectSwarmBeforeTurn(op, handle, err)
	} else if !exact {
		return m.rejectSwarmBeforeTurn(op, handle, rewind.ErrTarget)
	}

	snapshot, err := m.sessionSnapshotWithContext(nil)
	if err != nil {
		return m.rejectSwarmBeforeTurn(op, handle, err)
	}
	ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
	defer cancel()
	sourceVersion, err := m.observeSource(ctx, op.sessionID)
	if err != nil {
		return m.rejectSwarmBeforeTurn(op, handle, err)
	}

	prompt := handle.Spec.Prompt
	before := &beforeTurn{
		input: rewind.CaptureInput{
			ID: uuid.NewString(), SessionID: op.sessionID, Workspace: m.settings.WorkspaceRoot(),
			Boundary: rewind.Boundary{
				PromptID: op.promptID, PromptText: prompt, HasAttachments: false,
				SettledTurnID: m.settledTurnID, EventWatermark: m.settledWatermark,
				InitialContinuation: m.initialContinuation,
			},
			Transcript: canonical,
		},
		expectedActive: m.initialActive,
		expectedSource: sourceVersion,
		attachments:    nil,
		reservation:    reservation,
		swarm:          handle,
	}
	before.sourceWritten = new(atomic.Pointer[sourceWrite])
	m.liveOperation.source = &sourceObservation{version: sourceVersion.version,
		preceding: append(append([]*atomic.Pointer[sourceWrite](nil), sourceVersion.preceding...), before.sourceWritten)}

	return m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistFull, generation: m.transcriptGeneration,
		snapshot: snapshot, before: before, beforeGeneration: op.generation, sourceWritten: before.sourceWritten})
}

func (m *UI) rejectSwarmBeforeTurn(op liveTurnOperation, handle *engine.RecordedSwarmActivation, err error) tea.Cmd {
	return func() tea.Msg {
		return settledLiveTurnMsg{sessionID: op.sessionID, generation: op.generation,
			result: beforeTurnFailedMsg{prompt: handle.Spec.Prompt, err: err}}
	}
}
