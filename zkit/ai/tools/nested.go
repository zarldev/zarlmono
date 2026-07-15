package tools

import (
	"context"
	"time"
)

// NestedToolCall describes a child tool invocation performed inside a composite
// tool such as program. The child call is already being executed by the parent;
// observers use this only for progress/UI reporting.
type NestedToolCall struct {
	ParentID ToolCallID
	ChildID  ToolCallID
	Sequence int
	Call     ToolCall
	Started  time.Time
}

// NestedToolResult describes the terminal state of a child tool invocation
// performed inside a composite tool.
type NestedToolResult struct {
	NestedToolCall
	Result   *ToolResult
	Err      error
	Kind     Kind
	Error    string
	Duration time.Duration
}

// NestedToolObserver observes child tool calls made by composite tools. Methods
// must be non-blocking or quick; they run on the composite tool's execution path.
type NestedToolObserver interface {
	OnNestedToolStarted(context.Context, NestedToolCall)
	OnNestedToolFinished(context.Context, NestedToolResult)
}

type nestedToolObserverKey struct{}

// ContextWithNestedToolObserver returns a child context carrying obs.
func ContextWithNestedToolObserver(ctx context.Context, obs NestedToolObserver) context.Context {
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, nestedToolObserverKey{}, obs)
}

// NestedToolObserverFromContext returns the observer installed on ctx, if any.
func NestedToolObserverFromContext(ctx context.Context) NestedToolObserver {
	obs, _ := ctx.Value(nestedToolObserverKey{}).(NestedToolObserver)
	return obs
}
