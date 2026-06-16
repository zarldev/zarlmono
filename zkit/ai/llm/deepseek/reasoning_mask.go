package deepseek

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// keepReasoningMask decides, per message, whether DeepSeek-V4 requires
// the assistant turn's reasoning_content retained. V4's rule: between
// two user messages, reasoning_content must be echoed back iff that
// window contains a tool call; tool-call-free windows can drop it to
// save input tokens.
//
// We keep on doubt — dropping a required reasoning_content is a 400,
// while keeping a droppable one only costs a few tokens. So a window
// counts as "tool-active" (keep all its turns) if any assistant in it
// carries tool_calls or any tool result appears in it. An assistant
// turn that itself has tool_calls therefore always lands in a
// tool-active window and is never stripped.
//
// Installed on the wrapped openai provider via
// openai.WithReasoningKeepMask in NewProvider when the model uses
// Field mode.
func keepReasoningMask(messages []llm.Message) []bool {
	keep := make([]bool, len(messages))
	start := 0
	apply := func(end int) {
		toolActive := false
		for j := start; j < end; j++ {
			if messages[j].Role == llm.RoleTool ||
				(messages[j].Role == llm.RoleAssistant && len(messages[j].ToolCalls) > 0) {
				toolActive = true
				break
			}
		}
		for j := start; j < end; j++ {
			keep[j] = toolActive
		}
	}
	for i, msg := range messages {
		if msg.Role == llm.RoleUser {
			apply(i)
			start = i + 1
		}
	}
	apply(len(messages))
	return keep
}
