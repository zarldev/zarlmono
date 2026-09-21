package spawn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

var (
	// ErrParentClosed reports admission into a parent scope that has ended.
	ErrParentClosed = errors.New("agent parent scope is closed")
	// ErrParentExists reports an attempt to bind the same run more than once.
	ErrParentExists = errors.New("agent parent scope already exists")
)

// ParentScope is a value handle for one owning Run. Mutable state stays in
// Group; copying a handle does not copy lifecycle ownership. The caller must
// Close it after Run returns, before releasing borrowed dependencies.
type ParentScope struct {
	group *Group
	id    taskscope.ID
}

type parentState struct {
	deadline     time.Time
	sealed       bool
	stop         func() bool
	callbackDone chan struct{}
	changed      chan struct{}
}

// childLifetime is a cancellation/join snapshot, not mutable task state.
type childLifetime struct {
	cancel context.CancelCauseFunc
	done   <-chan struct{}
}

// Bind registers an owning run before it can spawn children. Children preserve
// dispatch context values but outlive dispatch cancellation; this scope links
// them to the parent run's cancellation and deadline instead. Unbound callers
// retain the legacy group-owned lifetime and explicit result delivery contract.
func (g *Group) Bind(ctx context.Context, id taskscope.ID) (ParentScope, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ParentScope{}, ErrGroupClosed
	}
	if err := context.Cause(ctx); err != nil {
		return ParentScope{}, fmt.Errorf("bind agent parent: %w", err)
	}
	if _, exists := g.parents[id]; exists {
		return ParentScope{}, fmt.Errorf("%w: %q", ErrParentExists, id)
	}
	return g.bindLocked(ctx, id), nil
}

func (g *Group) bindLocked(ctx context.Context, id taskscope.ID) ParentScope {
	deadline, _ := ctx.Deadline()
	scope := ParentScope{group: g, id: id}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		scope.cancel(context.Cause(ctx))
	})
	g.parents[id] = parentState{deadline: deadline, stop: stop, callbackDone: done, changed: make(chan struct{})}
	return scope
}

// cancel seals admission and requests direct-child cancellation. Each child's
// owned scope recursively cancels its descendants; joining stays with owners.
func (s ParentScope) cancel(cause error) []childLifetime {
	g := s.group
	g.mu.Lock()
	state := g.parents[s.id]
	state.sealed = true
	g.parents[s.id] = state
	g.signalLocked(s.id)
	var children []childLifetime
	for _, id := range g.order {
		t := g.tasks[id]
		if t.parent == s.id && t.snapshot.State == AgentTaskStates.RUNNING {
			children = append(children, childLifetime{cancel: t.cancel, done: t.done})
		}
	}
	g.mu.Unlock()
	for _, child := range children {
		child.cancel(cause)
	}
	return children
}

// Close seals admission, requests cancellation, stops and joins the cancellation
// callback, then joins direct children (which themselves join descendants).
// Cancellation of ctx only bounds this wait; Group remains the cleanup owner,
// and another Close can finish the join. Repeated and concurrent calls are safe.
func (s ParentScope) Close(ctx context.Context) error {
	children := s.cancel(context.Canceled)
	s.group.mu.Lock()
	state := s.group.parents[s.id]
	stop := state.stop
	// Claim the registration exactly once and release its captured operation
	// context. Nil means a Close already owns stopping it; every closer still
	// joins callbackDone below, including callers whose first wait timed out.
	state.stop = nil
	s.group.parents[s.id] = state
	s.group.mu.Unlock()
	if stop != nil && stop() {
		// Only the caller that stopped the registration owns closing this token.
		close(state.callbackDone)
	}
	select {
	case <-state.callbackDone:
	case <-ctx.Done():
		return fmt.Errorf("join agent parent callback: %w", context.Cause(ctx))
	}
	for _, child := range children {
		select {
		case <-child.done:
		case <-ctx.Done():
			return fmt.Errorf("join agent descendants: %w", context.Cause(ctx))
		}
	}
	return nil
}
