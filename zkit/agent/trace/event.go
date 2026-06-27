package trace

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// Kind classifies a trace event.
type Kind string

const (
	KindContent             Kind = "content"
	KindThinking            Kind = "thinking"
	KindToolStarted         Kind = "tool_started"
	KindToolCompleted       Kind = "tool_completed"
	KindToolFailed          Kind = "tool_failed"
	KindConversationStarted Kind = "conversation_started"
	KindConversationEnded   Kind = "conversation_ended"
	KindIterationCompleted  Kind = "iteration_completed"
	KindSteerInjected       Kind = "steer_injected"
	KindCompactionApplied   Kind = "compaction_applied"
	KindWorkflowStarted     Kind = "workflow_started"
	KindWorkflowNodeStarted Kind = "workflow_node_started"
	KindWorkflowNodeDone    Kind = "workflow_node_done"
	KindWorkflowNodeFailed  Kind = "workflow_node_failed"
	KindWorkflowCompleted   Kind = "workflow_completed"
	KindWorkflowFailed      Kind = "workflow_failed"
)

// Event is the normalized tracing representation for agent activity.
type Event struct {
	Time    time.Time    `json:"time"`
	Kind    Kind         `json:"kind"`
	TaskID  taskscope.ID `json:"task_id,omitempty"`
	Depth   int          `json:"depth,omitempty"`
	Name    string       `json:"name,omitempty"`
	Payload any          `json:"payload,omitempty"`
}
