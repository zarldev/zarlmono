// Package litellm implements explicitly selected LiteLLM gateway protocols using
// the native provider codecs. Protocol selection is configuration, not evidence
// of a deployment's native capabilities or exact continuation affinity.
package litellm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

// Provider routes requests through one explicitly configured gateway protocol.
// It does not infer serving identity, capability, or pricing from model aliases.
// Configure it before use; completion streams are synchronous and one-shot.
type Provider struct {
	apiKey        string
	baseURL       string
	model         string
	protocol      llm.ModelProtocol
	httpClient    *http.Client
	promptCache   openai.LiteLLMPromptCachePolicy
	messagesCache *messagesCacheDefaults
	chatReplay    *openai.LiteLLMReplayScope
}

var _ llm.Provider = (*Provider)(nil)

// NewProvider assembles a gateway provider with an already-resolved ordinary
// user key. It performs no I/O or SDK/transport setup. The default gateway root
// is http://localhost:4000, the protocol is Chat Completions, and model is empty.
// No environment credential lookup or model alias default is provided.
// Translated Chat replay continuity is local to this construction. A new
// provider (even with identical parameters) rejects earlier Chat continuation
// envelopes before HTTP; this is not durable session resume.
func NewProvider(apiKey string, opts ...options.Option[Provider]) *Provider {
	p := &Provider{chatReplay: &openai.LiteLLMReplayScope{}, apiKey: apiKey, baseURL: "http://localhost:4000", protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the LiteLLM adapter identity, not the native codec identity.
func (p *Provider) Name() string { return llm.LLMProviders.LITELLM.String() }

// Protocol returns the explicitly selected frontend protocol. This does not
// assert deployment support or native-versus-bridged backend fidelity.
func (p *Provider) Protocol() llm.ModelProtocol { return p.protocol }

// Complete constructs a fully lazy stream. SDK setup and its errors occur only
// during iteration. Native protocols retain codec continuation identities;
// Chat uses a distinct bounded translated envelope. Neither qualifies gateway
// deployment affinity or exact session-resume compatibility.
// Terminal codec errors are sanitized, preserving recognized llm classifications
// and rate-limit details. Caller cancellation causes are returned unchanged.
//
// Successful response headers are attached to the next ordinary codec chunk
// as Execution, never as an extra yield. Later body observations retain header
// identity; body cost takes precedence, with conflicts flagged in its Source.
// These are snapshots, not incremental charges: do not add ReportedCost to
// estimates of the same usage or sum repeated observations. A response
// with no ordinary chunks (including an immediate terminal error) emits no
// observation. Retaining consumers use CompletionChunk.Clone.
//
// Prefer affinity sends the host session ID as a best-effort gateway hint, not a
// pin guarantee. Require is rejected during iteration before any codec setup.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if cause := context.Cause(ctx); cause != nil {
			yield(llm.CompletionChunk{}, cause)
			return
		}
		if _, present := req.Options[openai.PromptCacheOption]; present {
			yield(llm.CompletionChunk{}, ErrCompletion)
			return
		}
		if req.Affinity.Mode == llm.AffinityModes.AFFINITYREQUIRE {
			yield(llm.CompletionChunk{}, ErrAffinityUnsupported)
			return
		}
		if p.protocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
			for _, message := range req.Messages {
				for _, item := range message.ContinuationItems {
					if item.Provider == "litellm" {
						yield(llm.CompletionChunk{}, ErrCompletion)
						return
					}
				}
			}
		}
		execution := executionState{model: p.model, apiKey: p.apiKey}
		if req.Affinity.Mode == llm.AffinityModes.AFFINITYPREFER {
			execution.session = req.Affinity.SessionID
		}
		codec, err := p.codec(p.requestClient(p.protocol, &execution))
		if err != nil {
			yield(llm.CompletionChunk{}, completionError(ctx, err))
			return
		}
		codec.Complete(ctx, req)(func(chunk llm.CompletionChunk, err error) bool {
			if err != nil {
				yield(llm.CompletionChunk{}, completionError(ctx, err))
				return false
			}
			if chunk.Execution != nil {
				observation := execution.mergeBody(*chunk.Execution)
				chunk.Execution = &observation
			} else if observation, ok := execution.take(); ok {
				chunk.Execution = &observation
			}
			return yield(chunk, nil)
		})
	}
}

func (p *Provider) codec(client *http.Client) (llm.Provider, error) {
	switch p.protocol {
	case llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLRESPONSES:
		opts := []options.Option[openai.Provider]{openai.WithBaseURL(strings.TrimRight(p.baseURL, "/") + "/v1"), openai.WithModel(p.model), openai.WithResponsesAPI(p.protocol == llm.ModelProtocols.PROTOCOLRESPONSES), openai.WithHTTPClient(client)}
		if p.protocol == llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
			opts = append(opts, openai.WithLiteLLMChat(), openai.WithLiteLLMReplayScope(p.chatReplay), openai.WithLiteLLMPromptCache(p.promptCache))
		}
		return openai.NewProvider(p.apiKey, opts...), nil
	case llm.ModelProtocols.PROTOCOLMESSAGES:
		opts := []options.Option[anthropic.Provider]{anthropic.WithBaseURL(strings.TrimRight(p.baseURL, "/") + "/"), anthropic.WithModel(p.model), anthropic.WithHTTPClient(client)}
		if p.messagesCache != nil {
			opts = append(opts, anthropic.WithCacheStrategy(p.messagesCache.strategy), anthropic.WithCacheTTLs(p.messagesCache.static, p.messagesCache.rolling))
		}
		return anthropic.NewProvider(p.apiKey, opts...), nil
	case llm.ModelProtocols.PROTOCOLGENERATECONTENT:
		provider, err := google.NewProvider(p.apiKey, google.WithBaseURL(strings.TrimRight(p.baseURL, "/")+"/"), google.WithModel(geminiModelResource(p.model)), google.WithHTTPClient(client))
		if err != nil {
			return nil, fmt.Errorf("litellm google codec: %w", err)
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("litellm protocol: %w", llm.ErrModelNotSupported)
	}
}

// The SDK accepts a full models/ resource as well as a short native name.
// Supply the resource explicitly so aliases starting with models/ or
// tunedModels/ are not mistaken for SDK namespaces. Escape the opaque alias
// as one path component; escaping dots also avoids SDK traversal heuristics
// and URL.JoinPath normalization changing a legitimate alias.
func geminiModelResource(alias string) string {
	return "models/" + strings.NewReplacer(".", "%2E", "&", "%26").Replace(url.PathEscape(alias))
}

// WithBaseURL sets the gateway root, optionally including a reverse-proxy path
// prefix, but excluding protocol/version suffixes such as /v1 or /v1beta.
// The configuration boundary must supply a validated HTTP(S) URL without user
// credentials, query, or fragment. This option only assigns the supplied value.
func WithBaseURL(baseURL string) options.Option[Provider] {
	return func(p *Provider) { p.baseURL = baseURL }
}

// WithModel sets the exact opaque gateway alias without canonicalization.
func WithModel(model string) options.Option[Provider] { return func(p *Provider) { p.model = model } }

// WithProtocol selects a frontend codec without model-name inference. Unknown
// and unsupported protocols yield ErrModelNotSupported lazily during iteration.
func WithProtocol(protocol llm.ModelProtocol) options.Option[Provider] {
	return func(p *Provider) { p.protocol = protocol }
}

// WithHTTPClient borrows client policy and its transport. Requests use an owned
// client copy with redirects disabled and request-local gateway authentication.
// The supplied client and request headers are never mutated. A nil Transport
// retains the standard http.DefaultTransport behavior. The caller owns transport
// shutdown and must not mutate client policy concurrently with provider use.
func WithHTTPClient(client *http.Client) options.Option[Provider] {
	return func(p *Provider) { p.httpClient = client }
}

// WithAnthropicPromptCache requests explicit Anthropic-compatible caching only
// for the selected LiteLLM Chat route. It is off by default; aliases never
// select this policy and request markers do not prove server cache honoring.
func WithAnthropicPromptCache(policy openai.LiteLLMPromptCachePolicy) options.Option[Provider] {
	return func(p *Provider) { p.promptCache = policy }
}

// messagesCacheDefaults is optional: nil retains the native codec factory defaults.
type messagesCacheDefaults struct {
	strategy        anthropic.CacheStrategy
	static, rolling anthropic.CacheTTL
}

// WithMessagesCachePolicy sets constructor defaults for the Messages codec only.
// Request-level Anthropic cache options still override these defaults.
func WithMessagesCachePolicy(strategy anthropic.CacheStrategy, static, rolling anthropic.CacheTTL) options.Option[Provider] {
	return func(p *Provider) { p.messagesCache = &messagesCacheDefaults{strategy, static, rolling} }
}
