// Package templates provides per-model chat-template helpers for local-LLM
// servers (llama.cpp, Ollama via /v1/) where the wire-format reasoning
// negotiation differs by model family.
//
// Two concerns live here:
//
//  1. Pre-flight message shaping. Some model families (Gemma-4) require an
//     inline "thinking" sentinel injected into the system message before
//     the request hits the wire. Others (Qwen3) leave messages untouched
//     and toggle reasoning purely via chat_template_kwargs.
//
//  2. The chat_template_kwargs payload. llama.cpp's Jinja templates inspect
//     this to decide whether to enable thinking. TemplateKwargs is the
//     typed form, kept off map[string]any at the edge.
//
// SplitThinking (in thinking.go) handles the inverse direction: extracting
// inline reasoning from a model's response when the wire format embeds it.
package templates

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ChatTemplate captures the per-model quirks a local-LLM client needs to
// negotiate. Implementations are value types — pass by copy, no shared
// state.
type ChatTemplate interface {
	// ShapeMessages returns a transformed messages slice ready for the
	// wire. The input slice is never mutated. When reasoning is true the
	// template is free to inject model-specific sentinels or system
	// entries; when false it must leave the logical prompt intact.
	ShapeMessages(msgs []llm.Message, reasoning bool) []llm.Message

	// ThinkingKwargs returns the chat_template_kwargs payload for the
	// request. The EnableThinking field maps to llama.cpp's Jinja
	// template variable of the same name.
	ThinkingKwargs(reasoning bool) Kwargs
}

// Kwargs is the typed payload serialized into the chat_template_kwargs
// request extension. It aliases llm.ChatTemplateKwargs so template helpers and
// completion requests use one provider-neutral typed payload.
type Kwargs = llm.ChatTemplateKwargs

// prependSystemSentinel returns a copy of messages with sentinel
// prepended to the first system message's content. When no system
// message exists, one is inserted at the head. Unexported — callers go
// through a template, not this helper directly.
func prependSystemSentinel(msgs []llm.Message, sentinel string) []llm.Message {
	if sentinel == "" {
		return msgs
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role == "system" {
			out[i].Content = sentinel + "\n" + out[i].Content
			return out
		}
	}
	return append([]llm.Message{{Role: "system", Content: sentinel}}, out...)
}
