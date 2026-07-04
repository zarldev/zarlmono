package runner

import (
	"context"
	"maps"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ObservedCall is one successful pure tool call recorded for a task.
type ObservedCall struct {
	ToolName  tools.ToolName
	Arguments tools.ToolParameters
}

// TaskCallLedger records successful pure tool calls per task so policy layers
// can ask what context the agent has already established in the current run.
type TaskCallLedger interface {
	RecordSuccessfulPureCall(ctx context.Context, tool tools.ToolName, args tools.ToolParameters)
	Calls(ctx context.Context) []ObservedCall
	ForgetTask(id taskscope.ID)
}

// MemoryTaskCallLedger is the in-memory TaskCallLedger implementation used by
// the production runner stack. Buckets are keyed by taskscope.ID; the zero ID is
// the shared "no task" bucket used by direct unit tests.
type MemoryTaskCallLedger struct {
	mu    sync.Mutex
	calls map[taskscope.ID][]ObservedCall
}

// NewMemoryTaskCallLedger builds an empty per-task call ledger.
func NewMemoryTaskCallLedger() *MemoryTaskCallLedger {
	return &MemoryTaskCallLedger{calls: make(map[taskscope.ID][]ObservedCall)}
}

// RecordSuccessfulPureCall appends one observed pure call to the current task.
func (l *MemoryTaskCallLedger) RecordSuccessfulPureCall(ctx context.Context, tool tools.ToolName, args tools.ToolParameters) {
	if l == nil {
		return
	}
	id := taskscope.IDFrom(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls[id] = append(l.calls[id], ObservedCall{ToolName: tool, Arguments: cloneToolParameters(args)})
}

// Calls returns a copy of the current task's observed-call slice.
func (l *MemoryTaskCallLedger) Calls(ctx context.Context) []ObservedCall {
	if l == nil {
		return nil
	}
	id := taskscope.IDFrom(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := l.calls[id]
	out := make([]ObservedCall, 0, len(seen))
	for _, call := range seen {
		out = append(out, ObservedCall{ToolName: call.ToolName, Arguments: cloneToolParameters(call.Arguments)})
	}
	return out
}

// ForgetTask drops the bucket for id.
func (l *MemoryTaskCallLedger) ForgetTask(id taskscope.ID) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.calls, id)
}

func cloneToolParameters(args tools.ToolParameters) tools.ToolParameters {
	if len(args) == 0 {
		return nil
	}
	out := make(tools.ToolParameters, len(args))
	maps.Copy(out, args)
	return out
}
