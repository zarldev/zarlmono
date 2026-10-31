package engine

import (
	"context"
	"errors"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrSwarmReceiver reports a provider route not verified for host observations.
var ErrSwarmReceiver = errors.New("provider does not support swarm observations")

// ErrSwarmWorkspace reports an attempted membership spanning different workspaces.
var ErrSwarmWorkspace = errors.New("swarm members must share one workspace")

// Swarm is the opt-in in-process engine adapter. It owns goal activation and one
// workspace coordinator across all member turns. LiveRunners are borrowed,
// dedicated to their member while the goal is open, and closed by the caller
// only after this owner drains. It does not attach background peers to the TUI's
// single-session settlement/persistence loop or claim restart durability.
type Swarm struct {
	mu          sync.Mutex
	goal        *swarm.Swarm
	coordinator *tools.WorkspaceCoordinator
	members     map[*LiveRunner]swarm.AgentID
	root        string
}

// NewSwarm assembles an opt-in goal owner without launching work. Configure each
// member LiveRunner's provider, permissions, limits and sink before Add.
func NewSwarm(goal string, opts ...options.Option[swarm.Swarm]) *Swarm {
	opts = append(opts, swarm.WithInferenceAccounting())
	return &Swarm{goal: swarm.New(goal, opts...), coordinator: tools.NewWorkspaceCoordinator(),
		members: make(map[*LiveRunner]swarm.AgentID)}
}

// Add registers a dedicated runtime for one persistent member. Its existing
// conversation is retained. One runtime cannot belong to two members of this
// owner, and all members must share the same workspace root.
func (s *Swarm) Add(id swarm.AgentID, live *LiveRunner, assignment string) (swarm.Member, error) {
	return s.register(id, live, assignment, func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
		return live.runSwarmActivation(ctx, activation, s.coordinator)
	})
}

// register admits one member under the shared membership and workspace checks,
// delegating activation execution to the supplied synchronous host capability.
func (s *Swarm) register(id swarm.AgentID, live *LiveRunner, assignment string, execute swarm.Execute) (swarm.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.members[live]; exists {
		return swarm.Member{}, swarm.ErrConflict
	}
	if len(s.members) > 0 && s.root != live.ws.Root() {
		return swarm.Member{}, ErrSwarmWorkspace
	}
	member, err := s.goal.Add(swarm.Agent{ID: id,
		Task:    runner.TaskSpec{Prompt: assignment, Context: live.ContextSnapshot()},
		Execute: execute,
	})
	if err != nil {
		return swarm.Member{}, err
	}
	s.members[live] = id
	s.root = live.ws.Root()
	return member, nil
}

// Run owns the activation workers until cancellation, Close, or budget exhaustion.
// Its return guarantees every turn and descendant drained before workspace release.
func (s *Swarm) Run(ctx context.Context) error {
	err := s.goal.Run(ctx)
	if !errors.Is(err, swarm.ErrStarted) {
		_ = s.goal.Close(context.WithoutCancel(ctx))
		s.coordinator.BeginShutdown()
	}
	return err
}

// Close stops and joins members before releasing their shared workspace coordinator.
// LiveRunners remain caller-owned. A timed-out wait never closes their dependencies.
func (s *Swarm) Close(ctx context.Context) error {
	if err := s.goal.Close(ctx); err != nil {
		return err
	}
	s.coordinator.BeginShutdown()
	return nil
}

// Post publishes host evidence and can activate idle members.
func (s *Swarm) Post(ctx context.Context, draft board.Draft) (board.Summary, error) {
	return s.goal.Post(ctx, draft)
}

// WaitIdle waits for all currently scheduled turns to settle without closing the goal.
func (s *Swarm) WaitIdle(ctx context.Context) error { return s.goal.WaitIdle(ctx) }

// Snapshot returns an independent member history and lifecycle view.
func (s *Swarm) Snapshot(id swarm.AgentID) (swarm.Snapshot, error) { return s.goal.Snapshot(id) }

// Stop permanently disables a member and requests cancellation of its current turn.
func (s *Swarm) Stop(id swarm.AgentID) error { return s.goal.Stop(id) }

type swarmTurn struct {
	activation  swarm.Activation
	coordinator *tools.WorkspaceCoordinator
}

func (l *LiveRunner) runSwarmActivation(ctx context.Context, activation swarm.Activation, coordinator *tools.WorkspaceCoordinator) runner.TaskResult {
	result := runner.TaskResult{ID: activation.Spec.ID, Reason: runner.TerminalError}
	reservation, err := l.ReserveTurn()
	if err != nil {
		result.Err = err
		return result
	}
	defer reservation.Release()
	return reservation.runSwarmActivation(ctx, activation, coordinator)
}

// runSwarmActivation dispatches one activation through a caller-held reservation,
// preserving activation identity, context transition, inputs/accounting, shared
// coordinator and descendant drain. The reservation is consumed on successful
// setup; on earlier failure the caller still owns it and must Release.
func (r *RuntimeReservation) runSwarmActivation(ctx context.Context, activation swarm.Activation, coordinator *tools.WorkspaceCoordinator, historyOptions ...options.Option[runner.Runner]) runner.TaskResult {
	result := runner.TaskResult{ID: activation.Spec.ID, Reason: runner.TerminalError}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		result.Err = ErrRuntimeBusy
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	converted := false
	var finish func()
	err := r.owner.context.transition(ctx, activation.Spec, func() (func(runner.TaskSpec) runner.TaskResult, error) {
		runCtx, end, setupErr := r.owner.beginTurn(ctx)
		if setupErr != nil {
			return nil, setupErr
		}
		finish = end
		turn, setupErr := r.owner.buildTurn(runCtx, r.owner.source, &swarmTurn{activation: activation, coordinator: coordinator}, append(historyOptions, runner.WithContextBreakdown())...)
		if setupErr != nil {
			return nil, setupErr
		}
		r.owner.admission.mu.Lock()
		r.owner.admission.active++
		r.owner.admission.reserved = false
		r.owner.admission.mu.Unlock()
		r.active = false
		converted = true
		return func(spec runner.TaskSpec) runner.TaskResult {
			defer turn.close(runCtx)
			spec.Thinking = turn.thinking
			scope, bindErr := turn.group.Bind(runCtx, spec.ID)
			if bindErr != nil {
				result = runner.TaskResult{ID: spec.ID, Reason: runner.TerminalError, Err: bindErr}
				return result
			}
			defer func() { _ = scope.Close(context.WithoutCancel(runCtx)) }()
			result = turn.inputs.Run(runCtx, func(receiveCtx context.Context) runner.TaskResult {
				return turn.runner.Run(receiveCtx, spec)
			})
			return result
		}, nil
	}, func() {
		if finish != nil {
			finish()
		}
		if converted {
			r.owner.admission.leave()
		}
	})
	if err != nil {
		result.Err = err
	}
	return result
}
