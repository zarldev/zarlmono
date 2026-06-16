// Package ollama provides an LLM provider for Ollama, which exposes an
// OpenAI-compatible /v1/* API surface alongside its native /api/* surface.
//
// The provider is a thin facade over pkg/ai/llm/openai with a sensible
// default BaseURL (http://localhost:11434/v1). Importing this package
// auto-registers the provider with the default LLM registry under
// llm.LLMProviders.OLLAMA.
//
// Note: this provider talks to Ollama's OpenAI-compatible endpoint, not its
// native one. Most production paths (chat, completion, tool use, embeddings)
// work fine on /v1/. If you need an Ollama-specific feature only available
// on /api/* (e.g. model pull / push), this provider is the wrong layer.
package ollama

import (
	"context"
	"iter"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

// DefaultBaseURL is the conventional Ollama OpenAI-compatible endpoint.
const DefaultBaseURL = "http://localhost:11434/v1"

// Provider is a thin Ollama-named facade over the OpenAI-compatible
// transport implementation.
type Provider struct {
	inner   llm.Provider
	baseURL string
	model   string
}

var _ llm.Provider = (*Provider)(nil)

// NewProvider creates an Ollama-targeted provider. Empty baseURL defaults
// to DefaultBaseURL. Ollama does not require an API key, but the openai
// SDK expects a non-empty value, so we supply a placeholder.
func NewProvider(opts ...options.Option[Provider]) (llm.Provider, error) {
	p := &Provider{
		baseURL: DefaultBaseURL,
	}
	for _, opt := range opts {
		opt(p)
	}

	innerOpts := []options.Option[openai.Provider]{
		openai.WithBaseURL(p.baseURL),
	}
	if p.model != "" {
		innerOpts = append(innerOpts, openai.WithModel(p.model))
	}

	inner, err := openai.NewProvider("ollama-no-auth", innerOpts...)
	if err != nil {
		return nil, err
	}
	p.inner = llm.Named(inner, "ollama")
	return p, nil
}

// Name returns the provider name.
func (p *Provider) Name() string { return "ollama" }

// Complete delegates to the underlying OpenAI-compatible provider.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return p.inner.Complete(ctx, req)
}

// WithBaseURL sets the Ollama API base URL. Empty string leaves the
// default in place.
func WithBaseURL(baseURL string) options.Option[Provider] {
	return func(p *Provider) {
		if baseURL != "" {
			p.baseURL = baseURL
		}
	}
}

// WithModel sets the default model for the provider.
func WithModel(model string) options.Option[Provider] {
	return func(p *Provider) {
		if model != "" {
			p.model = model
		}
	}
}
