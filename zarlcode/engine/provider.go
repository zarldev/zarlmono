package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/oauth/claude"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ProviderSpec is a resolved active-provider selection: which backend, the
// model, and optional overrides. BaseURL/APIKey override the registry's
// stored values when non-empty; CodexEffort tunes the Codex OAuth
// provider's reasoning effort.
type ProviderSpec struct {
	Name        string
	Model       string
	BaseURL     string
	APIKey      string
	CodexEffort string
	// Protocol is a runtime-only LiteLLM override, not persisted route state.
	// Unknown consults exact-alias policy, then Chat Completions. Never replace
	// this intent with resolved policy: a later alias must resolve independently.
	// Other adapters reject explicit values.
	Protocol llm.ModelProtocol
	// CachePolicy is runtime intent; inheritance resolves the destination alias.
	CachePolicy backends.RouteCachePolicy
}

// BuildProvider constructs an llm.Provider for ANY backend, dispatching on
// the authentication method each provider actually uses:
//
//   - openai-codex / claude-code (OAuth): built from a vault-backed token
//     source via prefs.Service — the credential + its refresh live in the
//     encrypted api_keys table, not a static key.
//   - everything else (openai Bearer, anthropic x-api-key + version,
//     deepseek, gemini, llamacpp / ollama local OpenAI-compatible): built
//     through the registry, which owns per-adapter construction and the
//     key-resolution chain (explicit override → credential service → local placeholder).
//
// This mirrors v1's buildProviderFor / buildLLMProvider split so the two
// front-ends can't drift on how a given backend is wired. The OAuth branch
// lives here (not in the registry) because only this layer holds the vault
// needed to build the token source — the registry deliberately refuses
// OAuth backends in BuildWithConfig.
func BuildProvider(ctx context.Context, reg *backends.ProviderRegistry, svc *prefs.Service, spec ProviderSpec) (llm.Provider, error) {
	built, err := buildProviderResolved(ctx, reg, svc, spec, spec.CachePolicy)
	if err != nil {
		return nil, err
	}
	return built.Provider, nil
}

// buildProviderResolved keeps credentials in the provider handle, not target facts.
// forced is transient for exact replay; spec retains its original runtime intent.
func buildProviderResolved(ctx context.Context, reg *backends.ProviderRegistry, svc *prefs.Service, spec ProviderSpec, forced backends.RouteCachePolicy) (backends.BuiltProvider, error) {
	switch id, _ := llm.ParseLLMProvider(spec.Name); id {
	case backends.NameOpenAICodex:
		if spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
			return backends.BuiltProvider{}, fmt.Errorf("%w: protocol override requires LiteLLM adapter", backends.ErrInvalidProviderDef)
		}
		if err := validateOAuthCachePolicy(forced); err != nil {
			return backends.BuiltProvider{}, err
		}
		if svc == nil {
			return backends.BuiltProvider{}, fmt.Errorf("%s: credential service unavailable", spec.Name)
		}
		var opts []options.Option[openaicodex.Provider]
		if spec.Model != "" {
			opts = append(opts, openaicodex.WithModel(spec.Model))
		}
		if spec.CodexEffort != "" {
			opts = append(opts, openaicodex.WithDefaultReasoningEffort(spec.CodexEffort))
		}
		return backends.BuiltProvider{Provider: openaicodex.NewProvider(codex.NewTokenSource(svc), opts...), Model: spec.Model}, nil
	case backends.NameClaudeCode:
		if spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
			return backends.BuiltProvider{}, fmt.Errorf("%w: protocol override requires LiteLLM adapter", backends.ErrInvalidProviderDef)
		}
		if err := validateOAuthCachePolicy(forced); err != nil {
			return backends.BuiltProvider{}, err
		}
		if svc == nil {
			return backends.BuiltProvider{}, fmt.Errorf("%s: credential service unavailable", spec.Name)
		}
		var opts []options.Option[claudecode.Provider]
		if spec.Model != "" {
			opts = append(opts, claudecode.WithModel(spec.Model))
		}
		return backends.BuiltProvider{Provider: claudecode.NewProvider(claude.NewTokenSource(svc), opts...), Model: spec.Model}, nil
	default:
		if reg == nil {
			return backends.BuiltProvider{}, errors.New("provider registry not initialised")
		}
		return reg.BuildResolved(ctx, spec.Name, backends.BuildConfig{
			BaseURL: spec.BaseURL, APIKey: spec.APIKey, Model: spec.Model,
			Protocol: spec.Protocol, CachePolicy: forced,
		})
	}
}

func validateOAuthCachePolicy(policy backends.RouteCachePolicy) error {
	if err := backends.ValidateRouteCachePolicy(policy); err != nil {
		return err
	}
	if policy.Mode != backends.RouteCacheModes.CACHEINHERIT && policy.Mode != backends.RouteCacheModes.CACHEDEFAULT {
		return backends.ErrInvalidRouteCachePolicy
	}
	return nil
}
