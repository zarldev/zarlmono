// Package llamacpp provides an LLM provider for llama.cpp's HTTP server,
// which exposes an OpenAI-compatible /v1/* API surface.
//
// The provider is a thin facade over pkg/ai/llm/openai with a sensible
// default BaseURL (http://localhost:8081/v1) and a stream-friendly
// HTTP client. Importing this package auto-registers the provider with
// the default LLM registry under llm.LLMProviders.LLAMACPP.
package llamacpp

import (
	"context"
	"iter"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

// DefaultBaseURL is the conventional llama.cpp server endpoint.
//
// 8081 (not 8080) is the zarlcode convention because 8080 is
// reserved for the bundled SearXNG container (docker/searxng/) and
// `zarlcode serve` boots llama-server on 8081. Override via
// WithBaseURL or LLAMACPP_BASE_URL when running on a different
// port.
const DefaultBaseURL = "http://localhost:8081/v1"

// Provider is a thin llama.cpp-named facade over the OpenAI-compatible
// transport implementation.
type Provider struct {
	inner   llm.Provider
	baseURL string
	model   string
	timeout time.Duration
}

var _ llm.Provider = (*Provider)(nil)

// NewProvider creates a llama.cpp-targeted provider. Empty baseURL defaults
// to DefaultBaseURL. The API key may be empty — llama.cpp's HTTP server
// does not require authentication by default — but the underlying openai SDK
// still expects a non-empty value, so we supply a placeholder.
//
// The transport-level timeouts come from the openai package's default
// client (zhttp.DefaultTransport). No whole-request timeout is set: local
// generations can run for many minutes, and http.Client.Timeout would cut
// off the SSE body mid-stream. Lifetime is governed by ctx instead.
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
	if p.timeout > 0 {
		innerOpts = append(innerOpts, openai.WithTimeout(p.timeout))
	}
	if p.model != "" {
		innerOpts = append(innerOpts, openai.WithModel(p.model))
	}

	inner, err := openai.NewProvider("llamacpp-no-auth", innerOpts...)
	if err != nil {
		return nil, err
	}
	p.inner = llm.Named(inner, "llamacpp")
	return p, nil
}

// Name returns the provider name.
func (p *Provider) Name() string { return "llamacpp" }

// Complete delegates to the underlying OpenAI-compatible provider.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return p.inner.Complete(ctx, req)
}

// WithBaseURL sets the llama.cpp server base URL. Empty string leaves the
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

// WithTimeout sets an explicit HTTP client timeout. Zero leaves the openai
// package default in place.
func WithTimeout(d time.Duration) options.Option[Provider] {
	return func(p *Provider) {
		p.timeout = d
	}
}
