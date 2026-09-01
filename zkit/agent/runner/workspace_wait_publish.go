package runner

import (
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type workspaceWaitPublisher struct {
	r    *Runner
	spec TaskSpec
	call tools.ToolCall
}

func (p workspaceWaitPublisher) OnWorkspaceWaitStarted(event tools.WorkspaceWaitStarted) {
	toolID, toolName := p.toolIdentity(event.Call)
	p.r.sink.OnWorkspaceWaitStarted(WorkspaceWaitStarted{
		TaskID: p.spec.ID, Depth: p.spec.Depth, ToolID: toolID, ToolName: toolName,
		Access: event.Access, Paths: append([]string(nil), event.Paths...), BlockerCount: len(event.Blockers),
		ParentToolID: event.Call.ParentToolID.String(), Sequence: event.Call.Sequence,
	})
}

func (p workspaceWaitPublisher) OnWorkspaceWaitEnded(event tools.WorkspaceWaitEnded) {
	toolID, toolName := p.toolIdentity(event.Call)
	p.r.sink.OnWorkspaceWaitEnded(WorkspaceWaitEnded{
		TaskID: p.spec.ID, Depth: p.spec.Depth, ToolID: toolID, ToolName: toolName,
		Outcome: event.Outcome, Duration: event.Waited,
		ParentToolID: event.Call.ParentToolID.String(), Sequence: event.Call.Sequence,
	})
}

func (p workspaceWaitPublisher) toolIdentity(call tools.WorkspaceWaitCall) (string, string) {
	if call.ToolID != "" {
		return call.ToolID.String(), call.ToolName.String()
	}
	return p.call.ID.String(), p.call.ToolName.String()
}
