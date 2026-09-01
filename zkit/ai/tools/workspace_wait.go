package tools

import (
	"context"
	"time"
)

// WorkspaceWaitBlocker identifies an active claim or earlier conflicting wait
// that is delaying workspace access.
type WorkspaceWaitBlocker struct {
	Owner  WorkspaceOwner
	Access WorkspaceAccess
	Paths  []string
}

// WorkspaceWaitCall identifies the tool call whose workspace access is waiting.
// Composite tools replace it while dispatching nested calls.
type WorkspaceWaitCall struct {
	ToolID       ToolCallID
	ToolName     ToolName
	ParentToolID ToolCallID
	Sequence     int
}

// WorkspaceWaitStarted describes a workspace request that entered the wait
// queue. It is emitted only after the request is known to be blocked.
type WorkspaceWaitStarted struct {
	Owner    WorkspaceOwner
	Access   WorkspaceAccess
	Paths    []string
	Blockers []WorkspaceWaitBlocker
	Call     WorkspaceWaitCall
	Started  time.Time
}

// WorkspaceWaitEnded describes the end of a workspace wait. Duration measures
// queue time only, not execution after the lease was granted.
type WorkspaceWaitEnded struct {
	Owner   WorkspaceOwner
	Access  WorkspaceAccess
	Paths   []string
	Call    WorkspaceWaitCall
	Outcome WorkspaceWaitOutcome
	Waited  time.Duration
}

// WorkspaceWaitObserver observes blocked workspace requests. Implementations
// must be safe for concurrent calls and must not assume callbacks are made
// while the coordinator lock is held.
type WorkspaceWaitObserver interface {
	OnWorkspaceWaitStarted(WorkspaceWaitStarted)
	OnWorkspaceWaitEnded(WorkspaceWaitEnded)
}

type workspaceWaitObserverKey struct{}
type workspaceWaitCallKey struct{}

// ContextWithWorkspaceWaitObserver returns a child context carrying observer.
func ContextWithWorkspaceWaitObserver(ctx context.Context, observer WorkspaceWaitObserver) context.Context {
	return context.WithValue(ctx, workspaceWaitObserverKey{}, observer)
}

// ContextWithWorkspaceWaitCall returns a child context attributing workspace
// waits to call. Composite tools use it for nested tool execution.
func ContextWithWorkspaceWaitCall(ctx context.Context, call WorkspaceWaitCall) context.Context {
	return context.WithValue(ctx, workspaceWaitCallKey{}, call)
}

// WorkspaceWaitObserverFromContext returns the observer installed on ctx, if any.
func WorkspaceWaitObserverFromContext(ctx context.Context) WorkspaceWaitObserver {
	observer, _ := ctx.Value(workspaceWaitObserverKey{}).(WorkspaceWaitObserver)
	return observer
}

func workspaceWaitCallFromContext(ctx context.Context) WorkspaceWaitCall {
	call, _ := ctx.Value(workspaceWaitCallKey{}).(WorkspaceWaitCall)
	return call
}

func (s WorkspaceWaitStarted) BlockerCount() int { return len(s.Blockers) }
