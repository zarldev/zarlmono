package runner

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type workspaceWaitPublisher struct {
	r      *Runner
	spec   TaskSpec
	call   tools.ToolCall
	nested *nestedToolPublisher
}

func (p workspaceWaitPublisher) OnWorkspaceWaitStarted(ctx context.Context, event tools.WorkspaceWaitStarted) {
	toolID, toolName := p.toolIdentity(event.Call)
	executionID, parentExecutionID := p.executionIdentity(event.Call)
	p.r.sink.OnWorkspaceWaitStarted(ctx, WorkspaceWaitStarted{
		TaskID: p.spec.ID, Depth: p.spec.Depth, ToolID: toolID, ToolName: toolName,
		Access: event.Access, Paths: append([]string(nil), event.Paths...), BlockerCount: len(event.Blockers),
		ExecutionID: executionID, ParentExecutionID: parentExecutionID, ParentToolID: event.Call.ParentToolID.String(), Sequence: event.Call.Sequence,
	})
}

func (p workspaceWaitPublisher) OnWorkspaceWaitEnded(ctx context.Context, event tools.WorkspaceWaitEnded) {
	toolID, toolName := p.toolIdentity(event.Call)
	executionID, parentExecutionID := p.executionIdentity(event.Call)
	p.r.sink.OnWorkspaceWaitEnded(ctx, WorkspaceWaitEnded{
		TaskID: p.spec.ID, Depth: p.spec.Depth, ToolID: toolID, ToolName: toolName,
		Outcome: event.Outcome, Duration: event.Waited,
		ExecutionID: executionID, ParentExecutionID: parentExecutionID, ParentToolID: event.Call.ParentToolID.String(), Sequence: event.Call.Sequence,
	})
}

func (p workspaceWaitPublisher) toolIdentity(call tools.WorkspaceWaitCall) (string, string) {
	if call.ToolID != "" {
		return call.ToolID.String(), call.ToolName.String()
	}
	return p.call.ID.String(), p.call.ToolName.String()
}

func (p workspaceWaitPublisher) executionIdentity(call tools.WorkspaceWaitCall) (string, string) {
	if call.ParentToolID != "" {
		return p.nested.executionForWait(call), p.call.ExecutionID
	}
	return p.call.ExecutionID, ""
}
