package service

// ChatTemplate captures the per-model quirks a llama-server client needs
// to negotiate: how to shape the messages array before sending and what
// chat_template_kwargs to set on the HTTP request. Implementations are
// value types — pass by copy, no shared state.
type ChatTemplate interface {
	// ShapeMessages returns a transformed messages slice ready for the
	// wire. The input slice is never mutated. When reasoning is true the
	// template is free to inject model-specific sentinels or system
	// entries; when false it must leave the logical prompt intact.
	ShapeMessages(msgs []Message, reasoning bool) []Message

	// ThinkingKwargs returns the chat_template_kwargs payload for the
	// request. The EnableThinking field maps to llama.cpp's Jinja
	// template variable of the same name.
	ThinkingKwargs(reasoning bool) TemplateKwargs
}

// TemplateKwargs is the typed payload we serialize into the
// chat_template_kwargs request extension. Kept as a concrete type so
// callers don't reach for map[string]any at the edge.
type TemplateKwargs struct {
	EnableThinking bool `json:"enable_thinking"`
}

// Gemma4Template implements ChatTemplate for unsloth's Gemma-4 builds.
// Gemma-4 uses an inline `<|think|>` sentinel at the head of the system
// message to flip its thinking channel on, and exposes EnableThinking
// via chat_template_kwargs for template-side gating.
type Gemma4Template struct{}

const gemma4ThinkSentinel = "<|think|>"

// ShapeMessages prepends the think sentinel when reasoning is enabled,
// creating a system message if none exists.
func (Gemma4Template) ShapeMessages(msgs []Message, reasoning bool) []Message {
	if !reasoning {
		return msgs
	}
	return prependSystemSentinel(msgs, gemma4ThinkSentinel)
}

// ThinkingKwargs reports EnableThinking=reasoning. Gemma-4's Jinja
// template reads the flag alongside the sentinel so both must be
// toggled together.
func (Gemma4Template) ThinkingKwargs(reasoning bool) TemplateKwargs {
	return TemplateKwargs{EnableThinking: reasoning}
}

// Qwen3Template implements ChatTemplate for Qwen3.x family models
// (including Qwen3.6-A3B). Qwen3 uses no inline sentinel — reasoning is
// toggled purely via chat_template_kwargs.enable_thinking, which its
// Jinja template inspects. Messages pass through unchanged.
type Qwen3Template struct{}

// ShapeMessages returns msgs unchanged. Qwen3 has no per-model
// decoration at the message layer.
func (Qwen3Template) ShapeMessages(msgs []Message, reasoning bool) []Message {
	return msgs
}

// ThinkingKwargs reports EnableThinking=reasoning. Qwen3's template
// branches on this alone; there is no sentinel to pair it with.
func (Qwen3Template) ThinkingKwargs(reasoning bool) TemplateKwargs {
	return TemplateKwargs{EnableThinking: reasoning}
}

// prependSystemSentinel returns a copy of messages with sentinel
// prepended to the first system message's content. When no system
// message exists, one is inserted at the head. Unexported — callers go
// through a template, not this helper directly.
func prependSystemSentinel(msgs []Message, sentinel string) []Message {
	if sentinel == "" {
		return msgs
	}
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role == "system" {
			out[i].Content = sentinel + "\n" + out[i].Content
			return out
		}
	}
	return append([]Message{{Role: "system", Content: sentinel}}, out...)
}
