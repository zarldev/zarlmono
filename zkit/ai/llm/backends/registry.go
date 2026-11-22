package backends

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/options"
)

// SettingsService supplies provider-keyed credentials. The consuming application
// owns scope, persistence, and unlocking; the registry has no environment fallback.
type SettingsService interface {
	GetKey(ctx context.Context, provider string) (string, error)
}

// ErrKeyNotFound reports that the settings service has no stored key.
var ErrKeyNotFound = errors.New("provider key not found")

// ProviderRegistry is the single source of truth for which LLM providers
// exist (built-in + Store-backed custom), how to build them, and how to
// resolve their credentials and settings.
type ProviderRegistry struct {
	store              Store
	routeProtocols     RouteProtocolStore
	routeCachePolicies RouteCachePolicyStore
	svc                SettingsService
	seeds              []ProviderDefinition

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

// WithSettingsService wires the caller-supplied provider-key credential lookup.
// Without it, credentials must be supplied explicitly in BuildConfig.
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

// NewRegistry creates a ProviderRegistry. The zero-option call includes only
// built-in provider definitions; it does not read ambient environment variables
// or resolve credentials. Callers supply provider configuration through
// BuildWithConfig and may wire persistence and credential lookup on top:
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
	case AdapterTypes.LITELLM:
		return liteLLM
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

// BuildConfig is the public override type for BuildWithConfig. Model and BaseURL
// may fall back to provider-definition defaults; APIKey comes from this config or
// the caller-supplied SettingsService, never the ambient environment.
type BuildConfig struct {
	Model string
	// BaseURL is a gateway root for LiteLLM, optionally with a reverse-proxy
	// prefix, but without a protocol/version suffix such as /v1 or /v1beta.
	BaseURL string
	APIKey  string
	// Protocol is runtime override intent only. Unknown consults exact-alias
	// policy, then defaults to Chat Completions. Other adapters reject overrides.
	Protocol llm.ModelProtocol
	// CachePolicy is runtime intent; zero inherits the saved exact-alias policy.
	CachePolicy RouteCachePolicy
}

// Build constructs an llm.Provider for the named definition, optionally
// overriding the model. Credentials come only from the supplied SettingsService.
func (r *ProviderRegistry) Build(ctx context.Context, name, model string) (llm.Provider, error) {
	return r.BuildWithConfig(ctx, name, BuildConfig{Model: model})
}

// BuiltProvider holds the actual provider and its construction-resolved route facts.
// It contains no credential or endpoint configuration.
// Protocol is the selected LiteLLM wire protocol; for native adapters Unknown
// means the protocol is managed by their runtime rather than selected here.
// CachePolicy is the effective construction policy, not BuildConfig intent.
type BuiltProvider struct {
	Provider    llm.Provider
	Model       string
	Protocol    llm.ModelProtocol
	CachePolicy RouteCachePolicy
}

// BuildWithConfig preserves the provider-only construction API.
func (r *ProviderRegistry) BuildWithConfig(ctx context.Context, name string, cfg BuildConfig) (llm.Provider, error) {
	built, err := r.BuildResolved(ctx, name, cfg)
	if err != nil {
		return nil, err
	}
	return built.Provider, nil
}

// BuildResolved resolves the exact alias and its policies once for this construction.
func (r *ProviderRegistry) BuildResolved(ctx context.Context, name string, cfg BuildConfig) (BuiltProvider, error) {
	def, err := r.Parse(name)
	if err != nil {
		return BuiltProvider{}, err
	}
	if cfg.Model == "" {
		cfg.Model = def.DefaultModel
	}
	if err := r.resolveConstructionProtocol(ctx, def, &cfg); err != nil {
		return BuiltProvider{}, err
	}
	policy, err := r.resolveConstructionCache(ctx, def, cfg.Model, cfg.CachePolicy)
	if err != nil {
		return BuiltProvider{}, err
	}
	if err := validateConstructionCache(def.AdapterType, cfg.Protocol, policy); err != nil {
		return BuiltProvider{}, err
	}
	policy = effectiveConstructionCache(def.AdapterType, cfg.Protocol, policy)
	provider, err := r.buildResolvedProvider(ctx, def, cfg, policy)
	if err != nil {
		return BuiltProvider{}, err
	}
	return BuiltProvider{Provider: provider, Model: cfg.Model, Protocol: cfg.Protocol, CachePolicy: policy}, nil
}

func (r *ProviderRegistry) resolveConstructionProtocol(ctx context.Context, def ProviderDefinition, cfg *BuildConfig) error {
	if def.AdapterType != AdapterTypes.LITELLM {
		if cfg.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
			return fmt.Errorf("%w: protocol override requires LiteLLM adapter", ErrInvalidProviderDef)
		}
		return nil
	}
	if cfg.Protocol == llm.ModelProtocols.PROTOCOLUNKNOWN {
		cfg.Protocol = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
		if r.routeProtocols != nil {
			// Validate the lookup key using the captured definition, not a second Parse.
			if err := validateConstructionRouteKey(def.Name, cfg.Model); err != nil {
				return err
			}
			protocol, err := r.routeProtocols.GetRouteProtocol(ctx, def.Name, cfg.Model)
			switch {
			case err == nil:
				cfg.Protocol = protocol
			case errors.Is(err, ErrRouteProtocolNotFound):
			default:
				return fmt.Errorf("get route protocol: %w", err)
			}
		}
	}
	return validateRouteProtocol(cfg.Protocol)
}

// validateConstructionRouteKey checks only the exact key used for a store read.
func validateConstructionRouteKey(provider, alias string) error {
	if provider == "" || alias == "" || !utf8.ValidString(provider) || !utf8.ValidString(alias) {
		return fmt.Errorf("%w: route protocol keys require nonempty UTF-8", ErrInvalidProviderDef)
	}
	return nil
}

func (r *ProviderRegistry) buildResolvedProvider(ctx context.Context, def ProviderDefinition, cfg BuildConfig, policy RouteCachePolicy) (llm.Provider, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = def.BaseURL
	}

	if def.AdapterType == AdapterTypes.LITELLM {
		if err := validateLiteLLMBaseURL(cfg.BaseURL); err != nil {
			return nil, err
		}
	}

	// OAuth-backed providers still route through the existing shell path.
	if def.AdapterType == AdapterTypes.OAUTHOPENAICODEX || def.AdapterType == AdapterTypes.OAUTHCLAUDECODE {
		return nil, errors.New("provider registry: OAuth-backed providers must be built via the shell path")
	}

	// Resolve API key.
	if cfg.APIKey == "" {
		var err error
		cfg.APIKey, err = r.resolveAPIKey(ctx, def)
		if err != nil {
			return nil, fmt.Errorf("resolve API key for %q: %w", def.Name, err)
		}
	}

	// Local OpenAI-compatible providers do not need auth, but the OpenAI SDK
	// insists on a non-empty API key. Use a harmless placeholder when the
	// definition declares no key requirement.
	if cfg.APIKey == "" && !def.RequiresKey() {
		cfg.APIKey = "zarlcode-no-auth"
	}

	if cfg.APIKey == "" && def.RequiresKey() {
		return nil, fmt.Errorf("provider %q: %w", def.Name, llm.ErrInvalidAPIKey)
	}

	at := adapterDiscriminator(def.AdapterType)
	p := buildParams{
		apiKey:           cfg.APIKey,
		baseURL:          cfg.BaseURL,
		model:            cfg.Model,
		protocol:         cfg.Protocol,
		cachePolicy:      policy,
		reasoningHistory: def.ReasoningHistory,
		// cache_prompt is a llama.cpp server extension. Enable it only for
		// the llama.cpp built-in — hosted OpenAI, Ollama, and custom
		// DB-backed providers (e.g. a LiteLLM proxy) reject the unknown
		// field with HTTP 400.
		cachePrompt:    def.Builtin && def.Name == DefaultBuiltinName.String(),
		templateKwargs: def.Builtin && (def.Name == DefaultBuiltinName.String() || def.Name == NameOllama.String()),
	}

	// Route through the adapter layer.
	switch at {
	case openAICompatible:
		ad, ok := lookupAdapter(at)
		if !ok {
			return nil, fmt.Errorf("%w: adapter %s in switch but absent from adapter table", ErrRegistryInternal, def.AdapterType)
		}
		prov, err := ad.build(ctx, p)
		if err != nil {
			return nil, err
		}
		id, _ := llm.ParseLLMProvider(def.Name)
		if !def.Builtin || id == DefaultBuiltinName || id == NameOllama {
			prov = llm.Named(prov, def.Name)
		}
		return prov, nil

	case deepSeekCompatible, liteLLM:
		ad, ok := lookupAdapter(at)
		if !ok {
			return nil, fmt.Errorf("%w: adapter %s in switch but absent from adapter table", ErrRegistryInternal, def.AdapterType)
		}
		prov, err := ad.build(ctx, p)
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
		prov, err := ad.build(ctx, p)
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
		prov, err := ad.build(ctx, p)
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

// resolveAPIKey consults only the credential source supplied by the caller.
func (r *ProviderRegistry) resolveAPIKey(ctx context.Context, def ProviderDefinition) (string, error) {
	if r.svc != nil {
		k, err := r.svc.GetKey(ctx, def.Name)
		if err == nil && k != "" {
			return k, nil
		}
		if err != nil && !errors.Is(err, ErrKeyNotFound) {
			return "", err
		}
	}
	return "", nil
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
// resolves the API key through the supplied credential service and probes the
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

// fetchLive probes the provider's model-list endpoint, resolving credentials
// internally. Gemini API discovery is filtered to generateContent-capable
// models; routes without usable generic discovery use their route-specific
// fallback catalogue.
func (r *ProviderRegistry) fetchLive(ctx context.Context, def ProviderDefinition) ([]string, error) {
	key, err := r.resolveAPIKey(ctx, def)
	if err != nil {
		return nil, fmt.Errorf("resolve API key for %q: %w", def.Name, err)
	}
	switch adapterDiscriminator(def.AdapterType) {
	case liteLLM:
		if key == "" {
			return nil, llm.ErrInvalidAPIKey
		}
		if err := validateLiteLLMBaseURL(def.BaseURL); err != nil {
			return nil, err
		}
		return fetchLiteLLMModels(ctx, def.BaseURL, key)
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
	case googleCompatible:
		return fetchGoogleModels(ctx, def.BaseURL, key)
	default:
		// Vertex discovery requires route-specific ADC/project configuration;
		// OAuth backends require a TokenSource the generic registry does not own.
		return def.SeedModels, nil
	}
}

// validateLiteLLMBaseURL validates external config before constructing a gateway
// provider. The URL is a gateway root, optionally with a reverse-proxy prefix,
// not a versioned protocol endpoint. Empty uses the provider's localhost default.
func validateLiteLLMBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid LiteLLM base URL", ErrInvalidProviderDef)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: LiteLLM base URL requires HTTP(S) without credentials, query or fragment", ErrInvalidProviderDef)
	}
	return nil
}
