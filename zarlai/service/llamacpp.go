package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

var (
	_ LLM                 = (*LlamaCppClient)(nil)
	_ StreamingChatClient = (*LlamaCppClient)(nil)
)

// LlamaCppClient talks to a llama-server OpenAI-compatible endpoint through
// the shared zkit openai provider. It keeps zarlai's per-model chat
// templating and vision handling, and delegates wire transport, reasoning
// extraction and tool-call assembly to zkit.
type LlamaCppClient struct {
	provider   llm.Provider
	baseURL    string
	model      string
	embedder   Embedder
	reasoning  bool
	template   ChatTemplate
	httpClient *http.Client
}

// Provider exposes the underlying zkit llm.Provider for the zkit-backed task
// loop; ChatClient stays the default surface.
func (l *LlamaCppClient) Provider() llm.Provider { return l.provider }

// LlamaCppOption customises a LlamaCppClient at construction.
type LlamaCppOption func(*LlamaCppClient)

// WithLlamaCppReasoning enables model reasoning mode. The active
// ChatTemplate decides how that maps to the wire (Gemma-4 prepends a
// `<|think|>` sentinel via ShapeMessages; both templates flip
// enable_thinking in chat_template_kwargs). zkit's provider surfaces the
// resulting reasoning_content as Thinking, kept out of Content.
func WithLlamaCppReasoning(v bool) LlamaCppOption {
	return func(c *LlamaCppClient) { c.reasoning = v }
}

// WithLlamaCppTemplate selects the per-model chat template strategy.
// Defaults to Gemma4Template; override to Qwen3Template (or future
// templates) when the loaded GGUF expects a different shaping.
func WithLlamaCppTemplate(t ChatTemplate) LlamaCppOption {
	return func(c *LlamaCppClient) { c.template = t }
}

// WithLlamaCppHTTPClient injects a custom *http.Client. Two LlamaCppClients
// sharing a llama-server but holding distinct *http.Client instances get
// independent connection pools and request state — required when the live
// conversation and the background task runner must not serialize against
// each other at the Go layer.
func WithLlamaCppHTTPClient(h *http.Client) LlamaCppOption {
	return func(c *LlamaCppClient) { c.httpClient = h }
}

// NewLlamaCppClient creates a client for llama-server. baseURL should point
// at the OpenAI-compatible endpoint (e.g. http://localhost:8081/v1).
func NewLlamaCppClient(baseURL, model string, embedder Embedder, opts ...LlamaCppOption) *LlamaCppClient {
	lc := &LlamaCppClient{
		baseURL:  baseURL,
		model:    model,
		embedder: embedder,
		template: Gemma4Template{},
	}
	for _, opt := range opts {
		opt(lc)
	}

	popts := []options.Option[openai.Provider]{
		openai.WithBaseURL(baseURL),
		openai.WithModel(model),
		openai.WithCachePrompt(true),
	}
	if lc.httpClient != nil {
		popts = append(popts, openai.WithHTTPClient(lc.httpClient))
	}
	// llama-server needs no auth; the provider only rejects an empty key.
	p, _ := openai.NewProvider("not-needed", popts...)
	lc.provider = p
	return lc
}

// request shapes messages through the active template and assembles the
// shared completion request, carrying enable_thinking in
// chat_template_kwargs.
func (l *LlamaCppClient) request(messages []Message, tools []llm.Tool, stream bool) llm.CompletionRequest {
	shaped := l.template.ShapeMessages(messages, l.reasoning)
	return llm.CompletionRequest{
		Messages:           toLLMMessages(shaped),
		Tools:              tools,
		Stream:             stream,
		ChatTemplateKwargs: templateKwargs(l.template, l.reasoning),
	}
}

// Chat sends messages to llama-server with vision and tool support and
// returns the batched result.
func (l *LlamaCppClient) Chat(ctx context.Context, messages []Message, tools []llm.Tool) (ChatResult, error) {
	return completeToResult(ctx, l.provider, l.request(messages, tools, false), "llamacpp chat")
}

// ChatStream streams incremental deltas from llama-server. Reasoning and
// content are emitted as they arrive; tool calls are resolved on the
// terminal delta.
func (l *LlamaCppClient) ChatStream(ctx context.Context, messages []Message, tools []llm.Tool) <-chan Delta {
	out := make(chan Delta, 16)
	go func() {
		defer close(out)

		chunks, err := l.provider.Complete(ctx, l.request(messages, tools, true))
		if err != nil {
			out <- Delta{Done: true, Err: fmt.Errorf("llamacpp stream: %w", err)}
			return
		}

		var content strings.Builder
		var acc toolCallAccumulator
		for chunk := range chunks {
			if chunk.Error != nil {
				out <- Delta{Done: true, Err: fmt.Errorf("llamacpp stream: %w", chunk.Error)}
				return
			}
			if len(chunk.ToolCalls) > 0 {
				acc.add(chunk.ToolCalls)
			}
			// Reasoning deltas carry Thinking (and duplicate it in Content);
			// route them to the Reasoning channel, never the content stream.
			if chunk.Thinking != "" {
				if !sendDelta(ctx, out, Delta{Reasoning: chunk.Thinking}) {
					return
				}
				continue
			}
			// Drop zkit's synthetic <think>/</think> boundary markers.
			if chunk.Content == "" || isThinkMarker(chunk.Content) {
				continue
			}
			content.WriteString(chunk.Content)
			if !sendDelta(ctx, out, Delta{Content: chunk.Content}) {
				return
			}
		}

		toolCalls, terr := finalizeStreamToolCalls(acc.calls(), content.String(), "llamacpp stream")
		if terr != nil {
			out <- Delta{Done: true, Err: terr}
			return
		}
		out <- Delta{Done: true, ToolCalls: toolCalls}
	}()
	return out
}

// Embed delegates to the underlying embedder (e.g. ollama).
func (l *LlamaCppClient) Embed(ctx context.Context, text string) ([]float32, error) {
	return l.embedder.Embed(ctx, text)
}
