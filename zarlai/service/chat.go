package service

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Message is a single message in a conversation.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Images    []string   `json:"images,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is a tool invocation requested by the model.
type ToolCall struct {
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the name and arguments of a tool call.
type FunctionCall struct {
	Name      string    `json:"name"`
	Arguments Arguments `json:"arguments"`
}

// Arguments is the decoded argument map of a model tool call (the LLM
// message layer). Tool implementations themselves consume the shared
// tools.ToolParameters; this is the wire-side decode the providers and the
// inline tool-call fallback produce, converted to a tools.ToolCall at
// dispatch time.
type Arguments map[string]any

// Get extracts a typed argument from a, returning the zero value when the
// key is missing or holds a different type.
func Get[T any](a Arguments, key string) T {
	v, ok := a[key].(T)
	if !ok {
		var zero T
		return zero
	}
	return v
}

// ChatResult is the response from an LLM chat call. Thinking is the model's
// internal reasoning, separated from Content so it never leaks into TTS or
// session history. Each provider client separates the two out of its own
// wire format (zkit surfaces native reasoning_content / thinking blocks as
// Thinking; the inline <think>…</think> fallback lives in completeToResult).
// Transport and task-runner layers treat both fields as already-separated.
type ChatResult struct {
	Content   string
	Thinking  string
	ToolCalls []ToolCall
}

// ChatClient is the interface for LLM chat providers.
type ChatClient interface {
	Chat(ctx context.Context, messages []Message, tools []llm.Tool) (ChatResult, error)
}

// ProviderAware is a ChatClient that also exposes its underlying zkit
// llm.Provider. The zkit-backed task loop needs the streaming
// llm.Provider directly (runner.Run drives Complete), which the batched
// ChatClient surface doesn't expose. The four built-in clients
// (anthropic/openai/ollama/llamacpp) satisfy it; a bare ChatClient (e.g. a
// test fake) may not, in which case the task runner falls back to the legacy
// loop.
type ProviderAware interface {
	ChatClient
	Provider() llm.Provider
}

// Embedder converts text to a vector embedding.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// LLM combines chat and embedding capabilities.
// Consumers that need both (e.g. the conversation server) depend on this.
type LLM interface {
	ChatClient
	Embedder
}

// Delta is one increment of a streamed LLM response. Non-terminal deltas
// carry at most one of Reasoning or Content (whichever the server just
// emitted). The terminal delta (Done=true) carries the final resolved
// ToolCalls and, if the stream aborted, a non-nil Err. A terminal delta
// always arrives and the channel always closes after it.
type Delta struct {
	Reasoning string
	Content   string
	ToolCalls []ToolCall
	Done      bool
	Err       error
}

// StreamingChatClient streams an LLM response incrementally. The returned
// channel yields Deltas until a terminal Delta{Done: true} is sent, after
// which it closes. Consumers should range over the channel and handle
// d.Err on terminal deltas.
//
// Not every provider needs to support streaming — callers that want a
// batched response continue to use ChatClient.Chat. Conversation paths
// sensitive to time-to-first-speech prefer ChatStream.
type StreamingChatClient interface {
	ChatStream(ctx context.Context, messages []Message, tools []llm.Tool) <-chan Delta
}
