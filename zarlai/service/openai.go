package service

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

var _ ChatClient = (*OpenAIClient)(nil)

// OpenAIClient talks to any OpenAI-compatible chat completions endpoint via
// the shared zkit openai provider.
type OpenAIClient struct {
	provider llm.Provider
	model    string
}

// Provider exposes the underlying zkit llm.Provider for the zkit-backed task
// loop; ChatClient stays the default surface.
func (o *OpenAIClient) Provider() llm.Provider { return o.provider }

// NewOpenAIClient creates an OpenAI-compatible client. baseURL can point to
// OpenAI, Groq, Together, Fireworks, etc. An empty apiKey is replaced with a
// placeholder so keyless local/proxy endpoints work — the provider only
// rejects an empty key.
func NewOpenAIClient(baseURL, apiKey, model string) *OpenAIClient {
	if apiKey == "" {
		apiKey = "not-needed"
	}
	// NewProvider only errors on an empty API key, which we guard above.
	p, _ := openai.NewProvider(apiKey,
		openai.WithBaseURL(baseURL),
		openai.WithModel(model),
	)
	return &OpenAIClient{provider: p, model: model}
}

// Chat sends messages to the OpenAI-compatible endpoint and returns the
// batched result. The shared provider handles reasoning extraction and
// native tool-call assembly; completeToResult adds the inline tool-call
// text fallback for pass-through backends that inline calls in content.
func (o *OpenAIClient) Chat(ctx context.Context, messages []Message, tools []llm.Tool) (ChatResult, error) {
	req := llm.CompletionRequest{
		Messages: toLLMMessages(messages),
		Tools:    tools,
	}
	return completeToResult(ctx, o.provider, req, "openai chat")
}
