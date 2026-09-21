package runner

import (
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// TaskTurnQuality optionally scopes a completion guard to the current run.
// Legacy Inspect remains available; the runner prefers InspectTask when provided
// so a shared guard never mistakes another parent's work for this task's work.
type TaskTurnQuality interface {
	TurnQuality
	InspectTask(taskscope.ID, string, []llm.ToolCall) TurnQualityDecision
}

func (t *taskRun) inspectTurnQuality(content string) TurnQualityDecision {
	if scoped, ok := t.r.turnQuality.(TaskTurnQuality); ok {
		return scoped.InspectTask(t.spec.ID, content, nil)
	}
	return t.r.turnQuality.Inspect(content, nil)
}
