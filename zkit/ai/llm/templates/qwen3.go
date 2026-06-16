package templates

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Qwen3 implements ChatTemplate for the Qwen3.x family (including
// Qwen3.6-A3B). Qwen3 uses no inline sentinel — reasoning is toggled
// purely via chat_template_kwargs.enable_thinking, which its Jinja
// template inspects. Messages pass through unchanged.
type Qwen3 struct{}

// ShapeMessages returns msgs unchanged. Qwen3 has no per-model
// decoration at the message layer.
func (Qwen3) ShapeMessages(msgs []llm.Message, reasoning bool) []llm.Message {
	return msgs
}

// ThinkingKwargs reports EnableThinking=reasoning, plus
// PreserveThinking=true whenever thinking is on. Qwen-team
// explicitly recommends preserve_thinking for agentic tool-call
// loops so the model sees its own prior reasoning across turns —
// without it, prior `<think>` blocks get stripped on input and the
// model hits a blank slate every iteration.
func (Qwen3) ThinkingKwargs(reasoning bool) Kwargs {
	return Kwargs{
		EnableThinking:   reasoning,
		PreserveThinking: reasoning,
	}
}

// Compile-time interface satisfaction check.
var _ ChatTemplate = Qwen3{}
