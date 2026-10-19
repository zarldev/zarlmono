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
	return &Swarm{goal: swarm.New(goal, opts...), coordinator: tools.NewWorkspaceCoordinator(),
		members: make(map[*LiveRunner]swarm.AgentID)}
}

// Add registers a dedicated runtime for one persistent member. Its existing
// conversation is retained. One runtime cannot belong to two members of this
// owner, and all members must share the same workspace root.
func (s *Swarm) Add(id swarm.AgentID, live *LiveRunner, assignment string) (swarm.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.members[live]; exists {
		return swarm.Member{}, swarm.ErrConflict
	}
	if len(s.members) > 0 && s.root != live.ws.Root() {
		return swarm.Member{}, ErrSwarmWorkspace
	}
	member, err := s.goal.Add(swarm.Agent{ID: id,
		Task: runner.TaskSpec{Prompt: assignment, Context: live.ContextSnapshot()},
		Execute: func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
			return live.runSwarmActivation(ctx, activation, s.coordinator)
		}})
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
	reservation.mu.Lock()
	defer reservation.mu.Unlock()

	converted := false
	var finish func()
	err = l.context.transition(ctx, activation.Spec, func() (func(runner.TaskSpec) runner.TaskResult, error) {
		runCtx, end, setupErr := l.beginTurn(ctx)
		if setupErr != nil {
			return nil, setupErr
		}
		finish = end
		turn, setupErr := l.buildTurn(runCtx, l.source, &swarmTurn{activation: activation, coordinator: coordinator}, runner.WithContextBreakdown())
		if setupErr != nil {
			return nil, setupErr
		}
		l.admission.mu.Lock()
		l.admission.active++
		l.admission.reserved = false
		l.admission.mu.Unlock()
		reservation.active = false
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
			l.admission.leave()
		}
	})
	if err != nil {
		result.Err = err
	}
	return result
}
