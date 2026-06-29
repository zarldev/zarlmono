package backends

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/options"
)

// SettingsService is the interface the registry needs for key
// resolution: the encrypted vault + provider-keyed lookup. Each
// consumer implements it (zarlcode wraps its settingsService).
type SettingsService interface {
	GetKey(ctx context.Context, provider string) (string, bool, error)
}

// ProviderRegistry is the single source of truth for which LLM providers
// exist (built-in + Store-backed custom), how to build them, and how to
// resolve their credentials and settings.
type ProviderRegistry struct {
	store Store
	svc   SettingsService
	seeds []ProviderDefinition

	mu              sync.RWMutex
	merged          []ProviderDefinition
	byName          map[string]ProviderDefinition
	activeName      string
	modelsDevSource *modelsdev.Source
}

// WithStore wires a Store of custom provider rows. Without it the
// registry is built-ins only.
func WithStore(store Store) options.Option[ProviderRegistry] {
	return func(r *ProviderRegistry) { r.store = store }
}

// WithSettingsService wires the vault-backed key lookup. Without it
// API keys resolve from environment variables alone.
func WithSettingsService(svc SettingsService) options.Option[ProviderRegistry] {
	return func(r *ProviderRegistry) { r.svc = svc }
}

// WithProviderDefinitions replaces the seed definitions. The default
// is BuiltinDefinitions().
func WithProviderDefinitions(defs []ProviderDefinition) options.Option[ProviderRegistry] {
	return func(r *ProviderRegistry) { r.seeds = defs }
}

// WithModelsDevSource wires an optional live model-info source
// (e.g. models.dev). When set, ResolveCost / ResolveCapabilities
// consult it between the per-provider DB override and the static
// per-package table.
func WithModelsDevSource(s *modelsdev.Source) options.Option[ProviderRegistry] {
	return func(r *ProviderRegistry) { r.modelsDevSource = s }
}

// NewRegistry creates a ProviderRegistry. The zero-option call is the
// zero-dependency configuration — built-in provider definitions, API
// keys from environment variables — which is what scripts, examples,
// and tests want. Applications wire persistence and key storage on
// top:
//
//	backends.NewRegistry(
//		backends.WithStore(store),
//		backends.WithSettingsService(svc),
//	)
func NewRegistry(opts ...options.Option[ProviderRegistry]) *ProviderRegistry {
	r := &ProviderRegistry{seeds: BuiltinDefinitions()}
	for _, opt := range opts {
		opt(r)
	}
	r.rebuild(r.seeds, nil)
	return r
}

func (r *ProviderRegistry) rebuild(builtins []ProviderDefinition, customs []StoredProvider) {
	byName := make(map[string]ProviderDefinition, len(builtins)+len(customs))
	merged := make([]ProviderDefinition, 0, len(builtins)+len(customs))

	for _, b := range builtins {
		byName[b.Name] = b
		merged = append(merged, b)
	}
	for _, c := range customs {
		at, err := ParseAdapterType(c.AdapterType)
		if err != nil {
			continue
		}
		d := ProviderDefinition{
			Name:              c.Name,
			DisplayName:       c.DisplayName,
			AdapterType:       at,
			BaseURL:           c.BaseURL,
			DefaultModel:      c.DefaultModel,
			SeedModels:        c.SeedModels,
			ReasoningHistory:  c.ReasoningHistory,
			ContextWindow:     c.ContextWindow,
			InputCostPerMTok:  c.InputCostPerMTok,
			OutputCostPerMTok: c.OutputCostPerMTok,
			Builtin:           c.Builtin,
			Enabled:           c.Enabled,
		}
		byName[d.Name] = d
		merged = append(merged, d)
	}

	r.mu.Lock()
	r.merged = merged
	r.byName = byName
	r.mu.Unlock()
}

// Reload re-reads custom provider rows from the Store and rebuilds the
// merged view atomically. Call after any mutation. A nil Store leaves
// the built-ins in place.
func (r *ProviderRegistry) Reload(ctx context.Context) error {
	if r.store == nil {
		r.rebuild(r.seeds, nil)
		return nil
	}
	rows, err := r.store.ListProviders(ctx)
	if err != nil {
		return err
	}
	r.rebuild(r.seeds, rows)
	return nil
}

// All returns a copy of the current merged provider list.
func (r *ProviderRegistry) All() []ProviderDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderDefinition, len(r.merged))
	copy(out, r.merged)
	return out
}

// adapterDiscriminator maps the generated AdapterType back to the
// package-level adapterType constant used for adapterRegistry lookups.
func adapterDiscriminator(at AdapterType) adapterType {
	switch at {
	case AdapterTypes.OPENAICOMPATIBLE:
		return openAICompatible
	case AdapterTypes.DEEPSEEKCOMPATIBLE:
		return deepSeekCompatible
	case AdapterTypes.ANTHROPICCOMPATIBLE:
		return anthropicCompatible
	case AdapterTypes.GOOGLECOMPATIBLE:
		return googleCompatible
	case AdapterTypes.GOOGLEVERTEX:
		return googleVertex
	case AdapterTypes.OAUTHOPENAICODEX:
		return oauthOpenAICodex
	case AdapterTypes.OAUTHCLAUDECODE:
		return oauthClaudeCode
	default:
		return -1
	}
}

// Parse looks up a provider by name in the merged set.
func (r *ProviderRegistry) Parse(name string) (ProviderDefinition, error) {
	r.mu.RLock()
	d, ok := r.byName[name]
	r.mu.RUnlock()
	if !ok {
		return ProviderDefinition{}, fmt.Errorf("%w: %q", ErrProviderNotFound, name)
	}
	if !d.Enabled {
		return d, ErrProviderDisabled
	}
	return d, nil
}

// BuildConfig is the public override type for BuildWithConfig. Empty fields
// fall back to the provider definition's defaults.
type BuildConfig struct {
	Model   string
	BaseURL string
	APIKey  string
}

// Build constructs an llm.Provider for the named definition, optionally
// overriding the model. It resolves the API key via the vault → env chain.
func (r *ProviderRegistry) Build(ctx context.Context, name, model string) (llm.Provider, error) {
	return r.BuildWithConfig(ctx, name, BuildConfig{Model: model})
}

// BuildWithConfig constructs an llm.Provider for name using cfg as an
// override layer. Empty cfg.Model falls back to def.DefaultModel; empty
// cfg.BaseURL falls back to def.BaseURL; empty cfg.APIKey resolves through
// the registry's vault/env chain.
func (r *ProviderRegistry) BuildWithConfig(ctx context.Context, name string, cfg BuildConfig) (llm.Provider, error) {
	def, err := r.Parse(name)
	if err != nil {
		return nil, err
	}
	if cfg.Model == "" {
		cfg.Model = def.DefaultModel
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = def.BaseURL
	}

	// OAuth-backed providers still route through the existing shell path.
	if def.AdapterType == AdapterTypes.OAUTHOPENAICODEX || def.AdapterType == AdapterTypes.OAUTHCLAUDECODE {
		return nil, errors.New("provider registry: OAuth-backed providers must be built via the shell path")
	}

	// Resolve API key.
	if cfg.APIKey == "" {
		cfg.APIKey = r.resolveAPIKey(ctx, def)
	}

	// Local OpenAI-compatible providers do not need auth, but the OpenAI SDK
	// insists on a non-empty API key. Use a harmless placeholder when the
	// definition declares no key requirement.
	if cfg.APIKey == "" && !def.RequiresKey() {
		cfg.APIKey = "zarlcode-no-auth"
	}

	at := adapterDiscriminator(def.AdapterType)
	p := buildParams{
		apiKey:           cfg.APIKey,
		baseURL:          cfg.BaseURL,
		model:            cfg.Model,
		reasoningHistory: def.ReasoningHistory,
		// cache_prompt is a llama.cpp server extension. Enable it only for
		// the llama.cpp built-in — hosted OpenAI, Ollama, and custom
		// DB-backed providers (e.g. a LiteLLM proxy) reject the unknown
		// field with HTTP 400.
		cachePrompt: def.Builtin && def.Name == DefaultBuiltinName.String(),
	}

	// Route through the adapter layer.
	switch at {
	case openAICompatible:
		ad, ok := lookupAdapter(at)
		if !ok {
			return nil, fmt.Errorf("%w: adapter %s in switch but absent from adapter table", ErrRegistryInternal, def.AdapterType)
		}
		prov, err := ad.build(p)
		if err != nil {
			return nil, err
		}
		id, _ := llm.ParseLLMProvider(def.Name)
		if !def.Builtin || id == DefaultBuiltinName || id == NameOllama {
			prov = llm.Named(prov, def.Name)
		}
		return prov, nil

	case deepSeekCompatible:
		ad, ok := lookupAdapter(at)
		if !ok {
			return nil, fmt.Errorf("%w: adapter %s in switch but absent from adapter table", ErrRegistryInternal, def.AdapterType)
		}
		prov, err := ad.build(p)
		if err != nil {
			return nil, err
		}
		if !def.Builtin {
			prov = llm.Named(prov, def.Name)
		}
		return prov, nil

	case anthropicCompatible:
		ad, ok := lookupAdapter(at)
		if !ok {
			return nil, fmt.Errorf("%w: adapter %s in switch but absent from adapter table", ErrRegistryInternal, def.AdapterType)
		}
		prov, err := ad.build(p)
		if err != nil {
			return nil, err
		}
		if !def.Builtin {
			prov = llm.Named(prov, def.Name)
		}
		return prov, nil

	case googleCompatible, googleVertex:
		ad, ok := lookupAdapter(at)
		if !ok {
			return nil, fmt.Errorf("%w: adapter %s in switch but absent from adapter table", ErrRegistryInternal, def.AdapterType)
		}
		prov, err := ad.build(p)
		if err != nil {
			return nil, err
		}
		if !def.Builtin {
			prov = llm.Named(prov, def.Name)
		}
		return prov, nil

	default:
		return nil, fmt.Errorf("%w: unsupported adapter type %v", ErrRegistryInternal, def.AdapterType)
	}
}

// resolveAPIKey walks the key resolution chain for def:
//  1. Vault (workspace then global)
//  2. Provider-specific env vars
//  3. Generic LLM_API_KEY env fallback
func (r *ProviderRegistry) resolveAPIKey(ctx context.Context, def ProviderDefinition) string {
	// Vault rows are keyed by provider name and apply to every provider —
	// built-in or DB-backed custom. Gating this read on RequiresKey() (as
	// we used to) meant a key saved for a custom provider was silently
	// never used, because customs declare no env-var sources and so always
	// report RequiresKey() == false.
	if r.svc != nil {
		if k, ok, _ := r.svc.GetKey(ctx, def.Name); ok && k != "" {
			return k
		}
	}
	// Env-var fallbacks only apply to providers that declare them (the
	// hosted built-ins). Providers with no declared env vars — local
	// backends and customs — must not inherit an unrelated LLM_API_KEY.
	if len(def.EnvAPIKeyVars) == 0 {
		return ""
	}
	for _, v := range def.EnvAPIKeyVars {
		if k := os.Getenv(v); k != "" {
			return k
		}
	}
	return os.Getenv("LLM_API_KEY")
}

// SetActiveName records the currently active provider so Delete can
// protect the running session.
func (r *ProviderRegistry) SetActiveName(name string) {
	r.mu.Lock()
	r.activeName = name
	r.mu.Unlock()
}

// Delete removes a custom provider. Rejects built-ins and the active provider.
func (r *ProviderRegistry) Delete(ctx context.Context, name string) error {
	def, err := r.Parse(name)
	if err != nil {
		return err
	}
	if def.Builtin {
		return ErrProviderBuiltin
	}
	r.mu.RLock()
	active := r.activeName
	r.mu.RUnlock()
	if active == name {
		return ErrProviderActive
	}
	if r.store == nil {
		return ErrNoStore
	}
	return r.store.DeleteProvider(ctx, name)
}

// UpsertProvider inserts or replaces a custom provider definition and
// reloads the merged view. DB-encoding concerns (JSON seed lists,
// timestamps) live in the Store implementation, not here.
func (r *ProviderRegistry) UpsertProvider(ctx context.Context, def ProviderDefinition) error {
	if err := ValidateDefinition(def); err != nil {
		return err
	}
	if r.store == nil {
		return ErrNoStore
	}
	err := r.store.UpsertProvider(ctx, StoredProvider{
		Name:              def.Name,
		DisplayName:       def.DisplayName,
		AdapterType:       def.AdapterType.String(),
		BaseURL:           def.BaseURL,
		DefaultModel:      def.DefaultModel,
		SeedModels:        def.SeedModels,
		ReasoningHistory:  def.ReasoningHistory,
		ContextWindow:     def.ContextWindow,
		InputCostPerMTok:  def.InputCostPerMTok,
		OutputCostPerMTok: def.OutputCostPerMTok,
		Enabled:           def.Enabled,
		Builtin:           def.Builtin,
	})
	if err != nil {
		return err
	}
	return r.Reload(ctx)
}

// FetchModels returns the live model list for a provider by name. It
// resolves the API key internally (vault → env chain) and probes the
// provider's /models endpoint based on adapter type. On any failure — or
// when the probe returns nothing — it falls back to def.SeedModels so the
// picker always has something to show and is never blocked by a down or
// unauthenticated provider. The caller is expected to pass a ctx with a
// deadline (see modelListFetchTimeout) to keep the picker snappy.
func (r *ProviderRegistry) FetchModels(ctx context.Context, name string) ([]string, error) {
	def, err := r.Parse(name)
	if err != nil {
		return nil, err
	}
	models, ferr := r.fetchLive(ctx, def)
	if ferr == nil && len(models) > 0 {
		return models, nil
	}
	if len(def.SeedModels) > 0 {
		return def.SeedModels, nil
	}
	return models, ferr
}

// fetchLive probes the provider's /models endpoint for the adapter type,
// resolving the API key internally. Google and OAuth providers have no
// usable live probe, so they return their seed list directly.
func (r *ProviderRegistry) fetchLive(ctx context.Context, def ProviderDefinition) ([]string, error) {
	key := r.resolveAPIKey(ctx, def)
	switch adapterDiscriminator(def.AdapterType) {
	case openAICompatible, deepSeekCompatible:
		// Send a bearer whenever a key resolved — hosted built-ins and any
		// custom provider with a key saved in the vault. Probe
		// unauthenticated only when there's genuinely no key (local
		// llamacpp/ollama). Keying off the resolved value rather than
		// RequiresKey() is what lets custom hosted providers authenticate.
		if key != "" {
			return fetchOpenAIBearerModels(ctx, def.BaseURL, key)
		}
		return fetchOpenAICompatModels(ctx, def.BaseURL, key)
	case anthropicCompatible:
		return fetchAnthropicModels(ctx, def.BaseURL, key)
	default:
		// googleCompatible curates its list; OAuth backends need a TokenSource
		// the generic registry does not own, so they return seeds here.
		return def.SeedModels, nil
	}
}
