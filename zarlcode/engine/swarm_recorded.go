package engine

import (
	"context"
	"errors"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/db"
)

// ErrSwarmActivationNotRun reports a recorded activation whose host callback
// returned without dispatching RunRecorded.
var ErrSwarmActivationNotRun = errors.New("recorded swarm activation was not run")

// RecordedSwarmActivation is a single-use, callback-scoped recorded activation
// handle. AddRecorded passes one to the host executor for each activation. The
// engine owns one runtime reservation and the authoritative activation identity;
// the host captures BEFORE state through Reservation, dispatches exactly once
// through RunRecorded, and settles durable session writes before returning.
type RecordedSwarmActivation struct {
	// Reservation is borrowed for checkpoint capture before RunRecorded. The
	// engine owns its release; the host must not Release it or dispatch through
	// its ordinary RunTurn methods.
	Reservation *RuntimeReservation

	// Spec is an independently owned metadata snapshot of the dispatched
	// activation. Editing it does not change the executed activation.
	Spec runner.TaskSpec

	mu          sync.Mutex
	activation  swarm.Activation
	coordinator *tools.WorkspaceCoordinator
	live        *LiveRunner
	run         bool
	released    bool
	result      runner.TaskResult
}

// RunRecorded dispatches this activation through the borrowed reservation with
// canonical replay capture against store/sessionID. It is single-use: a second
// call, a call after the executor returned, or a call after engine release all
// return an unsuccessful result without dispatching. The returned TaskResult is
// the complete engine result; the host must not fabricate its own.
func (a *RecordedSwarmActivation) RunRecorded(ctx context.Context, store *db.Store, sessionID string) runner.TaskResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.released || a.run {
		return runner.TaskResult{ID: a.activation.Spec.ID, Reason: runner.TerminalError, Err: ErrRuntimeBusy}
	}
	a.run = true
	result := a.Reservation.runSwarmActivation(ctx, a.activation, a.coordinator, a.live.sessionRecordingOptions(store, sessionID)...)
	a.result = result
	return result
}

// AddRecorded registers a recorded foreground member. Unlike Add, the host
// executor wraps the full settlement lifecycle: the engine reserves admission,
// the callback captures BEFORE state, dispatches RunRecorded, then waits for
// applied events and a durable full-save acknowledgment before returning. The
// callback's error joins the engine result and suspends the member on failure.
func (s *Swarm) AddRecorded(id swarm.AgentID, live *LiveRunner, assignment string, execute func(context.Context, *RecordedSwarmActivation) error) (swarm.Member, error) {
	return s.register(id, live, assignment, func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
		return s.runRecorded(ctx, live, activation, execute)
	})
}

func (s *Swarm) runRecorded(ctx context.Context, live *LiveRunner, activation swarm.Activation, execute func(context.Context, *RecordedSwarmActivation) error) runner.TaskResult {
	reservation, err := live.ReserveTurn()
	if err != nil {
		return runner.TaskResult{ID: activation.Spec.ID, Reason: runner.TerminalError, Err: err}
	}
	defer reservation.Release()

	spec := activation.Spec
	spec.Context = llm.CloneMessages(spec.Context)
	spec.Attachments = llm.CloneContentParts(spec.Attachments)
	handle := &RecordedSwarmActivation{
		Reservation: reservation,
		Spec:        spec,
		activation:  activation,
		coordinator: s.coordinator,
		live:        live,
	}

	hostErr := execute(ctx, handle)

	handle.mu.Lock()
	ran := handle.run
	result := handle.result
	handle.released = true
	handle.mu.Unlock()

	if !ran {
		result = runner.TaskResult{ID: activation.Spec.ID, Reason: runner.TerminalError, Err: ErrSwarmActivationNotRun}
	}
	if hostErr != nil {
		if result.Err != nil {
			result.Err = errors.Join(result.Err, hostErr)
		} else {
			result.Err = hostErr
		}
		result.Reason = runner.TerminalError
	}
	return result
}
