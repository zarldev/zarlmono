package service

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/options"
)

var _ ChatClient = (*AnthropicClient)(nil)

// AnthropicOption configures an AnthropicClient.
type AnthropicOption func(*anthropicConfig)

type anthropicConfig struct {
	baseURL string
}

// WithAnthropicBaseURL overrides the Anthropic API base URL (used in tests).
func WithAnthropicBaseURL(url string) AnthropicOption {
	return func(c *anthropicConfig) {
		c.baseURL = url
	}
}

// AnthropicClient talks to the Anthropic Messages API via the shared zkit
// anthropic provider.
type AnthropicClient struct {
	provider llm.Provider
	model    string
}

// Provider exposes the underlying zkit llm.Provider so the zkit-backed task
// loop can drive runner.Run directly instead of the batched Chat
// path. ChatClient stays the default surface for the live conversation.
func (a *AnthropicClient) Provider() llm.Provider { return a.provider }

// NewAnthropicClient creates an Anthropic client. An empty apiKey is
// replaced with a placeholder so the constructor stays non-erroring — the
// provider only rejects an empty key.
func NewAnthropicClient(apiKey, model string, opts ...AnthropicOption) *AnthropicClient {
	cfg := &anthropicConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if apiKey == "" {
		apiKey = "not-needed"
	}

	popts := []options.Option[anthropic.Provider]{anthropic.WithModel(model)}
	if cfg.baseURL != "" {
		popts = append(popts, anthropic.WithBaseURL(cfg.baseURL))
	}
	// NewProvider only errors on an empty API key, which we guard above.
	p, _ := anthropic.NewProvider(apiKey, popts...)
	return &AnthropicClient{provider: p, model: model}
}

// Chat sends messages to the Anthropic Messages API and returns the result.
// The shared provider lifts the system message out of the slice, maps
// tool_use blocks to tool calls and native thinking blocks to Thinking;
// completeToResult adds the inline tool-call text fallback.
func (a *AnthropicClient) Chat(ctx context.Context, messages []Message, tools []llm.Tool) (ChatResult, error) {
	req := llm.CompletionRequest{
		Messages:  toLLMMessages(messages),
		Tools:     tools,
		MaxTokens: 4096,
	}
	return completeToResult(ctx, a.provider, req, "anthropic chat")
}
