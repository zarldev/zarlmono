package templates

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const gemma4ThinkSentinel = "<|think|>"

// Gemma4 implements ChatTemplate for unsloth's Gemma-4 builds. Gemma-4
// uses an inline `<|think|>` sentinel at the head of the system message
// to flip its thinking channel on, and exposes EnableThinking via
// chat_template_kwargs for template-side gating.
type Gemma4 struct{}

// ShapeMessages prepends the think sentinel when reasoning is enabled,
// creating a system message if none exists.
func (Gemma4) ShapeMessages(msgs []llm.Message, reasoning bool) []llm.Message {
	if !reasoning {
		return msgs
	}
	return prependSystemSentinel(msgs, gemma4ThinkSentinel)
}

// ThinkingKwargs reports EnableThinking=reasoning. Gemma-4's Jinja
// template reads the flag alongside the sentinel so both must be
// toggled together.
func (Gemma4) ThinkingKwargs(reasoning bool) Kwargs {
	return Kwargs{EnableThinking: reasoning}
}

// Compile-time interface satisfaction check.
var _ ChatTemplate = Gemma4{}
