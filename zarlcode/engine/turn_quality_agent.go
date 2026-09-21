package engine

import (
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// agentAwareTurnQuality prevents a root run from claiming completion while its
// owned child tasks are still running or their terminal summaries are unread.
type agentAwareTurnQuality struct {
	base  runner.TurnQuality
	group *spawn.Group
}

// NewAgentAwareTurnQuality composes base with the owned-agent completion guard.
func NewAgentAwareTurnQuality(base runner.TurnQuality, group *spawn.Group) runner.TurnQuality {
	return agentAwareTurnQuality{base: base, group: group}
}

func (q agentAwareTurnQuality) Inspect(content string, toolCalls []llm.ToolCall) runner.TurnQualityDecision {
	var tasks []spawn.TaskSnapshot
	if q.group != nil {
		tasks = q.group.Outstanding()
	}
	return q.inspect("", tasks, content, toolCalls)
}

func (q agentAwareTurnQuality) InspectTask(id taskscope.ID, content string, toolCalls []llm.ToolCall) runner.TurnQualityDecision {
	var tasks []spawn.TaskSnapshot
	if q.group != nil {
		tasks = q.group.OutstandingFor(id)
	}
	return q.inspect(id, tasks, content, toolCalls)
}

func (q agentAwareTurnQuality) inspect(id taskscope.ID, tasks []spawn.TaskSnapshot, content string, toolCalls []llm.ToolCall) runner.TurnQualityDecision {
	if len(toolCalls) == 0 && len(tasks) > 0 {
		ids := make([]string, 0, len(tasks))
		for _, task := range tasks {
			ids = append(ids, string(task.ID))
		}
		return runner.TurnQualityDecision{Correction: fmt.Sprintf(
			"Agent tasks are still running or have unread results: %s. Continue independent work, inspect with agent_status, or join with agent_await before giving the final answer.",
			strings.Join(ids, ", "))}
	}
	if q.base == nil {
		return runner.TurnQualityDecision{}
	}
	if scoped, ok := q.base.(runner.TaskTurnQuality); ok {
		return scoped.InspectTask(id, content, toolCalls)
	}
	return q.base.Inspect(content, toolCalls)
}

var (
	_ runner.TurnQuality     = agentAwareTurnQuality{}
	_ runner.TaskTurnQuality = agentAwareTurnQuality{}
)
