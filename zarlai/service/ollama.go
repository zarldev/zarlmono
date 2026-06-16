package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
)

var _ LLM = (*OllamaClient)(nil)

// OllamaClient talks to Ollama through the shared zkit provider (Ollama's
// OpenAI-compatible /v1 surface) and delegates embeddings to a separate
// Embedder (typically OpenAIEmbedder pointed at /v1/embeddings on the same
// Ollama instance).
type OllamaClient struct {
	provider  llm.Provider
	model     string
	embedder  Embedder
	reasoning bool
	template  ChatTemplate
}

// Provider exposes the underlying zkit llm.Provider for the zkit-backed task
// loop; ChatClient stays the default surface.
func (o *OllamaClient) Provider() llm.Provider { return o.provider }

// OllamaOption customises an OllamaClient at construction.
type OllamaOption func(*OllamaClient)

// WithOllamaReasoning enables reasoning mode on outgoing chat requests. The
// selected ChatTemplate (default Gemma4Template) decides how to signal that
// to the model via chat_template_kwargs.
func WithOllamaReasoning(v bool) OllamaOption {
	return func(c *OllamaClient) { c.reasoning = v }
}

// WithOllamaTemplate selects the per-model chat template strategy. Defaults
// to Gemma4Template.
func WithOllamaTemplate(t ChatTemplate) OllamaOption {
	return func(c *OllamaClient) { c.template = t }
}

// NewOllamaClient creates an Ollama client. baseURL may be the host root
// (e.g. http://localhost:11434) or already include /v1 — it is normalised to
// the OpenAI-compatible endpoint zkit's provider expects. The embedder may
// be nil for chat-only callers (Embed then fails with a clear error).
func NewOllamaClient(baseURL, model string, embedder Embedder, opts ...OllamaOption) *OllamaClient {
	c := &OllamaClient{
		model:    model,
		embedder: embedder,
		template: Gemma4Template{},
	}
	for _, opt := range opts {
		opt(c)
	}
	// ollama.NewProvider only fails on a malformed config we don't supply.
	p, _ := ollama.NewProvider(
		ollama.WithBaseURL(ensureOpenAIV1(baseURL)),
		ollama.WithModel(model),
	)
	c.provider = p
	return c
}

// Embed delegates to the injected Embedder so OllamaClient satisfies the LLM
// interface (chat + embed) the same way LlamaCppClient does.
func (o *OllamaClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if o.embedder == nil {
		return nil, fmt.Errorf("ollama client: no embedder configured")
	}
	return o.embedder.Embed(ctx, text)
}

// Chat sends messages to Ollama and returns the result. Pass nil for tools
// to make a tool-free call.
func (o *OllamaClient) Chat(ctx context.Context, messages []Message, tools []llm.Tool) (ChatResult, error) {
	shaped := o.template.ShapeMessages(messages, o.reasoning)
	req := llm.CompletionRequest{
		Messages:           toLLMMessages(shaped),
		Tools:              tools,
		ChatTemplateKwargs: templateKwargs(o.template, o.reasoning),
	}
	return completeToResult(ctx, o.provider, req, "ollama chat")
}

// ensureOpenAIV1 normalises an Ollama base URL to the OpenAI-compatible /v1
// endpoint zkit's provider talks to. An empty URL is left empty so the
// provider falls back to its own default.
func ensureOpenAIV1(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if trimmed == "" || strings.HasSuffix(trimmed, "/v1") {
		return trimmed
	}
	return trimmed + "/v1"
}
