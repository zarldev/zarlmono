package harness

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/oauth/claude"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
)

// providerEnv bundles the zarlcode-side state the in-process driver
// needs to build a provider the same way the TUI does: the state.db
// (custom-provider rows + encrypted vault) and the prefs service that
// resolves API keys and OAuth credentials. Opened once per driver and
// shared across tasks — providers are safe for concurrent Complete
// calls, so one client serves every parallel worker.
type providerEnv struct {
	store *db.Store
	svc   *prefs.Service
}

// openProviderEnv opens zarlcode's state.db + vault. A missing vault is
// non-fatal: providers that need no secret (llamacpp, ollama) build
// fine without one, and the registry's key resolution falls through to
// env vars. stateDB empty resolves to ~/.zarlcode/state.db (db.Open's
// default).
func openProviderEnv(ctx context.Context, stateDB string) (*providerEnv, error) {
	store, err := db.Open(ctx, stateDB)
	if err != nil {
		return nil, fmt.Errorf("open state.db: %w", err)
	}
	// Eval is non-interactive: pass nil so the vault opens only from
	// $ZARLCODE_KEY / $ZARLCODE_PASSPHRASE (or an existing legacy key);
	// otherwise it stays disabled and provider keys fall back to env vars.
	var v *vault.Vault
	if opened, vErr := vault.Open(nil); vErr == nil {
		v = opened
	}
	// Global scope: codex/claude OAuth credentials and most API keys are
	// stored global (workspace=""), so effective resolution finds them
	// without a workspace pin.
	return &providerEnv{store: store, svc: prefs.NewService(store, v, "")}, nil
}

func (e *providerEnv) close() {
	if e != nil && e.store != nil {
		_ = e.store.Close()
	}
}

// settingsAdapter adapts prefs.Service to backends.SettingsService, the
// small key-resolution surface the provider registry needs. Mirrors
// zarlcode's providerSettingsAdapter — resolving at effective scope so
// a workspace key wins over the global default, falling back to global.
type settingsAdapter struct{ svc *prefs.Service }

func (a settingsAdapter) HasVault() bool { return a.svc != nil && a.svc.HasVault() }

func (a settingsAdapter) GetKey(ctx context.Context, provider string) (string, bool, error) {
	if a.svc == nil {
		return "", false, nil
	}
	return a.svc.GetKey(ctx, prefs.ScopeEffective, provider)
}

// buildProvider constructs the llm.Provider for (name, model). OAuth
// providers (openai-codex, claude-code) build from a vault-backed token
// source; everything else routes through the provider registry, which
// owns static construction and the vault→env key-resolution chain. This
// is the same branching zarlcode's buildProviderFor uses, so eval lands
// on the identical provider the TUI would for a given (name, model).
func (e *providerEnv) buildProvider(ctx context.Context, name, model, codexEffort string) (llm.Provider, error) {
	switch id, _ := llm.ParseLLMProvider(name); id {
	case backends.NameOpenAICodex:
		tokens := codex.NewTokenSource(e.svc)
		opts := []options.Option[openaicodex.Provider]{openaicodex.WithModel(model)}
		if codexEffort != "" {
			opts = append(opts, openaicodex.WithDefaultReasoningEffort(codexEffort))
		}
		return openaicodex.NewProvider(tokens, opts...)
	case backends.NameClaudeCode:
		tokens := claude.NewTokenSource(e.svc)
		return claudecode.NewProvider(tokens, claudecode.WithModel(model))
	default:
		reg := backends.NewRegistry(backends.WithStore(e.store), backends.WithSettingsService(settingsAdapter{svc: e.svc}))
		if err := reg.Reload(ctx); err != nil {
			return nil, fmt.Errorf("reload provider registry: %w", err)
		}
		// Empty name falls through to the registry default (llamacpp) via
		// Parse; empty model falls back to the definition's default model.
		buildName := name
		if buildName == "" {
			buildName = backends.DefaultBuiltinName.String()
		}
		return reg.BuildWithConfig(ctx, buildName, backends.BuildConfig{Model: model})
	}
}
