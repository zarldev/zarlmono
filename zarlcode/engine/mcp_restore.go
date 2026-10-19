package engine

import (
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// RestoreSessionConversation publishes an exact durable conversation and its
// current session-local selection hints as one reserved runtime transition.
// Historical replay controls never supply these live selection hints.
func (r *RuntimeReservation) RestoreSessionConversation(messages []llm.Message, savedTarget rewind.Target, plan code.Plan, target RunTarget, activations MCPActivations) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.dispatchOnly {
		return ErrRuntimeBusy
	}
	return r.restoreConversationState(messages, savedTarget, plan, target, &activations)
}
