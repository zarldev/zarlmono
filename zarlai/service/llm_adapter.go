package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// toLLMMessages converts zarlai conversation messages into the shared
// llm.Message form the zkit providers consume. Our internal ToolCall has no
// ID, so we mint deterministic ones and thread them onto the matching
// tool-result messages — the OpenAI wire format (and llama.cpp's OAI shim)
// reject tool messages without a tool_call_id.
func toLLMMessages(msgs []Message) []llm.Message {
	toolCallIDs := collectToolCallIDs(msgs)
	out := make([]llm.Message, 0, len(msgs))
	toolResultIdx := 0
	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			if m.Content == "" && len(m.ToolCalls) == 0 {
				continue
			}
			lm := llm.Message{Role: llm.RoleAssistant, Content: m.Content}
			for i, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Function.Arguments)
				lm.ToolCalls = append(lm.ToolCalls, llm.ToolCall{
					ID:   deterministicToolCallID(tc.Function.Name, i),
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: string(argsJSON),
					},
				})
			}
			out = append(out, lm)
		case "tool":
			id := ""
			if toolResultIdx < len(toolCallIDs) {
				id = toolCallIDs[toolResultIdx]
				toolResultIdx++
			}
			out = append(out, llm.Message{Role: llm.RoleTool, Content: m.Content, ToolCallID: id})
		case "system":
			out = append(out, llm.Message{Role: llm.RoleSystem, Content: m.Content})
		default: // user and anything unrecognised
			out = append(out, userLLMMessage(m))
		}
	}
	return out
}

// userLLMMessage builds a user llm.Message, attaching base64 images as
// multimodal parts when present. Mirrors the old openai-go content-parts
// construction: low-detail JPEG data URIs.
func userLLMMessage(m Message) llm.Message {
	if len(m.Images) == 0 {
		return llm.Message{Role: llm.RoleUser, Content: m.Content}
	}
	parts := make([]llm.ContentPart, 0, 1+len(m.Images))
	if m.Content != "" {
		parts = append(parts, llm.TextPart(m.Content))
	}
	for _, img := range m.Images {
		part := llm.ImagePartFromDataURI("data:image/jpeg;base64,"+img, "image/jpeg")
		part.Image.Detail = "low"
		parts = append(parts, part)
	}
	return llm.Message{Role: llm.RoleUser, Parts: parts}
}

// collectToolCallIDs walks messages and returns tool_call_id values in the
// order the tool-result messages reference them — one per assistant tool
// call, in order.
func collectToolCallIDs(messages []Message) []string {
	var ids []string
	for _, m := range messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		for i, tc := range m.ToolCalls {
			ids = append(ids, deterministicToolCallID(tc.Function.Name, i))
		}
	}
	return ids
}

// deterministicToolCallID produces a stable ID for a tool call from its name
// and position, since our internal ToolCall carries no ID field.
func deterministicToolCallID(name string, index int) string {
	return fmt.Sprintf("call_%s_%d", name, index)
}

// parseArguments unmarshals the JSON string arguments from a provider tool
// call into an Arguments map.
func parseArguments(raw string) (Arguments, error) {
	if raw == "" {
		return Arguments{}, nil
	}
	var args Arguments
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("unmarshal arguments %q: %w", raw, err)
	}
	return args, nil
}

// toServiceToolCalls converts shared llm.ToolCalls (JSON-string args) into
// zarlai ToolCalls (decoded Arguments map) the tool dispatch layer needs.
func toServiceToolCalls(raw []llm.ToolCall, label string) ([]ToolCall, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]ToolCall, 0, len(raw))
	for i, tc := range raw {
		args, err := parseArguments(tc.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("%s: parse tool call %d arguments: %w", label, i, err)
		}
		out = append(out, ToolCall{Function: FunctionCall{Name: tc.Function.Name, Arguments: args}})
	}
	return out, nil
}

// toolCallAccumulator reassembles streamed tool calls. zkit forwards tool
// calls as deltas keyed by ID — the first carries the name, later ones carry
// argument fragments — so callers must accumulate per ID rather than
// overwrite. The non-stream path emits a single complete fragment, which
// this handles identically.
type toolCallAccumulator struct {
	order []string
	byID  map[string]*llm.ToolCall
}

func (a *toolCallAccumulator) add(frags []llm.ToolCall) {
	if a.byID == nil {
		a.byID = make(map[string]*llm.ToolCall)
	}
	for _, f := range frags {
		tc, ok := a.byID[f.ID]
		if !ok {
			tc = &llm.ToolCall{ID: f.ID, Type: "function"}
			a.byID[f.ID] = tc
			a.order = append(a.order, f.ID)
		}
		if f.Function.Name != "" {
			tc.Function.Name = f.Function.Name
		}
		tc.Function.Arguments += f.Function.Arguments
	}
}

func (a *toolCallAccumulator) calls() []llm.ToolCall {
	out := make([]llm.ToolCall, 0, len(a.order))
	for _, id := range a.order {
		out = append(out, *a.byID[id])
	}
	return out
}

// isThinkMarker reports whether a content chunk is one of zkit's synthetic
// <think>/</think> boundary markers (emitted around reasoning_content so a
// SplitThinking-based consumer sees the canonical inline format). zarlai
// surfaces reasoning via the separate Reasoning/Thinking channel, so these
// markers are dropped from the content stream.
func isThinkMarker(s string) bool { return s == "<think>" || s == "</think>" }

// completeToResult drives a provider completion to a batched ChatResult. It
// drains the chunks, accumulating clean content and reasoning, then resolves
// tool calls — falling back to the inline <tool_call> text parser for models
// that emit calls in content rather than as native tool_calls.
func completeToResult(ctx context.Context, p llm.Provider, req llm.CompletionRequest, label string) (ChatResult, error) {
	chunks, err := p.Complete(ctx, req)
	if err != nil {
		return ChatResult{}, fmt.Errorf("%s: %w", label, err)
	}
	var content, thinking strings.Builder
	var acc toolCallAccumulator
	for chunk := range chunks {
		if chunk.Error != nil {
			return ChatResult{}, fmt.Errorf("%s: %w", label, chunk.Error)
		}
		if len(chunk.ToolCalls) > 0 {
			acc.add(chunk.ToolCalls)
		}
		if chunk.Thinking != "" {
			thinking.WriteString(chunk.Thinking)
			continue
		}
		if isThinkMarker(chunk.Content) {
			continue
		}
		content.WriteString(chunk.Content)
	}

	// Defensive split: batched providers separate reasoning already, but a
	// pass-through backend may still inline <think> tags in content.
	clean, inline := SplitThinking(content.String())
	result := ChatResult{Content: strings.TrimSpace(clean), Thinking: thinking.String()}
	if result.Thinking == "" {
		result.Thinking = inline
	}
	result.ToolCalls, err = toServiceToolCalls(acc.calls(), label)
	if err != nil {
		return ChatResult{}, err
	}
	if len(result.ToolCalls) == 0 && result.Content != "" {
		if parsed, remaining := ParseToolCallsFromText(result.Content); len(parsed) > 0 {
			result.ToolCalls = parsed
			result.Content = remaining
		}
	}
	return result, nil
}

// sendDelta forwards d on out unless ctx is cancelled. Returns false if the
// caller should abandon the stream (context cancelled) — a terminal error
// delta has already been sent in that case.
func sendDelta(ctx context.Context, out chan<- Delta, d Delta) bool {
	select {
	case out <- d:
		return true
	case <-ctx.Done():
		out <- Delta{Done: true, Err: ctx.Err()}
		return false
	}
}

// finalizeStreamToolCalls resolves accumulated tool calls from a stream,
// falling back to inline text parsing when the provider surfaced none (some
// Qwen configs emit calls as inline <tool_call> JSON inside content).
func finalizeStreamToolCalls(raw []llm.ToolCall, content, label string) ([]ToolCall, error) {
	calls, err := toServiceToolCalls(raw, label)
	if err != nil {
		return nil, err
	}
	if len(calls) > 0 {
		return calls, nil
	}
	if parsed, _ := ParseToolCallsFromText(content); len(parsed) > 0 {
		return parsed, nil
	}
	return nil, nil
}

// templateKwargs renders a ChatTemplate's thinking kwargs into the
// provider-neutral chat_template_kwargs map zkit serialises onto the wire.
func templateKwargs(t ChatTemplate, reasoning bool) map[string]any {
	kw := t.ThinkingKwargs(reasoning)
	return map[string]any{"enable_thinking": kw.EnableThinking}
}
