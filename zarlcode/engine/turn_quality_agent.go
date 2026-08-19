package engine

import (
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
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
	if len(toolCalls) == 0 && q.group != nil {
		if tasks := q.group.Outstanding(); len(tasks) > 0 {
			ids := make([]string, 0, len(tasks))
			for _, task := range tasks {
				ids = append(ids, string(task.ID))
			}
			return runner.TurnQualityDecision{Correction: fmt.Sprintf(
				"Agent tasks are still running or have unread results: %s. Continue independent work, inspect with agent_status, or join with agent_await before giving the final answer.",
				strings.Join(ids, ", "))}
		}
	}
	if q.base == nil {
		return runner.TurnQualityDecision{}
	}
	return q.base.Inspect(content, toolCalls)
}

var _ runner.TurnQuality = agentAwareTurnQuality{}
