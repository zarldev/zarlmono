package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/oauth/claude"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// ProviderSpec is a resolved active-provider selection: which backend, the
// model, and optional overrides. BaseURL/APIKey override the registry's
// stored/env values when non-empty; CodexEffort tunes the Codex OAuth
// provider's reasoning effort.
type ProviderSpec struct {
	Name        string
	Model       string
	BaseURL     string
	APIKey      string
	CodexEffort string
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
//     key-resolution chain (override → vault → env → placeholder).
//
// This mirrors v1's buildProviderFor / buildLLMProvider split so the two
// front-ends can't drift on how a given backend is wired. The OAuth branch
// lives here (not in the registry) because only this layer holds the vault
// needed to build the token source — the registry deliberately refuses
// OAuth backends in BuildWithConfig.
func BuildProvider(ctx context.Context, reg *backends.ProviderRegistry, svc *prefs.Service, spec ProviderSpec) (llm.Provider, error) {
	switch id, _ := llm.ParseLLMProvider(spec.Name); id {
	case backends.NameOpenAICodex:
		if svc == nil {
			return nil, fmt.Errorf("%s: credential service unavailable", spec.Name)
		}
		opts := []options.Option[openaicodex.Provider]{openaicodex.WithModel(spec.Model)}
		if spec.CodexEffort != "" {
			opts = append(opts, openaicodex.WithDefaultReasoningEffort(spec.CodexEffort))
		}
		return openaicodex.NewProvider(codex.NewTokenSource(svc), opts...)
	case backends.NameClaudeCode:
		if svc == nil {
			return nil, fmt.Errorf("%s: credential service unavailable", spec.Name)
		}
		return claudecode.NewProvider(claude.NewTokenSource(svc), claudecode.WithModel(spec.Model))
	default:
		if reg == nil {
			return nil, errors.New("provider registry not initialised")
		}
		return reg.BuildWithConfig(ctx, spec.Name, backends.BuildConfig{
			BaseURL: spec.BaseURL,
			APIKey:  spec.APIKey,
			Model:   spec.Model,
		})
	}
}
