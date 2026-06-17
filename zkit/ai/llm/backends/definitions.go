package backends

import (
	"context"
	"os"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ProviderDefinition is the public shape the registry surfaces to
// callers: the merged set of built-in + DB-backed providers.
type ProviderDefinition struct {
	Name        string
	DisplayName string
	AdapterType AdapterType
	BaseURL     string
	// BaseURLEnv is the env var that overrides BaseURL at resolution
	// time (e.g. LLAMACPP_BASE_URL). Empty for providers whose endpoint
	// is fixed by the SDK or pinned in BaseURL. Built-ins only — custom
	// DB-backed providers carry their absolute URL in BaseURL.
	BaseURLEnv    string
	DefaultModel  string
	SeedModels    []string
	EnvAPIKeyVars []string // e.g. ["OPENAI_API_KEY"]
	// ReasoningHistory is how this provider echoes prior-turn assistant
	// reasoning back in request history. The zero value is INLINE (today's
	// default); thinking models added as custom providers (Moonshot/Kimi)
	// use FIELD. Built-ins that need a non-default policy (DeepSeek) set it
	// inside their dedicated adapter, so this is primarily for DB-backed
	// custom providers.
	ReasoningHistory llm.ReasoningHistory
	// ContextWindow is the provider's declared context window in tokens. 0
	// means unknown — resolution falls back to the per-model table, a runtime
	// probe (llamacpp/ollama), or the compiled-in default. Primarily for
	// DB-backed custom providers whose models aren't in any static table
	// (e.g. Kimi K2.x at 262144).
	ContextWindow int
	// InputCostPerMTok / OutputCostPerMTok are the token price in USD per
	// 1,000,000 tokens. 0 means unmetered/unknown — resolution falls back to
	// the per-model price table. Primarily for DB-backed custom providers.
	InputCostPerMTok  float64
	OutputCostPerMTok float64
	Builtin           bool
	Enabled           bool
}

// RequiresKey reports whether this provider declares env-var sources for
// its API key (the hosted built-ins do; local backends like llamacpp and
// ollama don't). It drives the env-var fallback chain and the keyless
// build placeholder — NOT whether the provider authenticates at all. Use
// UsesAPIKey for that.
func (d ProviderDefinition) RequiresKey() bool {
	return len(d.EnvAPIKeyVars) > 0
}

// UsesAPIKey reports whether a key field should be offered for this
// provider in the UI. Built-ins signal it by declaring env-var sources;
// DB-backed custom providers are hosted endpoints assumed to take a key
// (they declare no env vars, so RequiresKey alone would hide the field).
// Local built-ins (llamacpp/ollama) and OAuth backends are neither, so
// they report false.
func (d ProviderDefinition) UsesAPIKey() bool {
	return d.RequiresKey() || !d.Builtin
}

// DefaultBuiltinName is the provider the shell falls back to when
// LLM_PROVIDER is unset — the local llama.cpp server. The goenums enum
// value is the source of truth; call .String() for the wire/DB name.
var DefaultBuiltinName = llm.LLMProviders.LLAMACPP

// Built-in providers that callers branch on (OAuth construction, codex
// prompt-cache wiring). These hold the goenums enum value so the wire
// name lives in exactly one place (the enum); reference them — and
// .String() at the wire/DB boundary — rather than open-coding the name.
var (
	NameOpenAICodex = llm.LLMProviders.OPENAICODEX
	NameClaudeCode  = llm.LLMProviders.CLAUDECODE
	NameOllama      = llm.LLMProviders.OLLAMA
)

// Builtin returns the built-in ProviderDefinition for name from the
// pure, DB-free catalogue. The env-bootstrap path (loadConfig) uses
// this to resolve a provider before the registry/DB exist. ok=false
// for unknown names.
func Builtin(name string) (ProviderDefinition, bool) {
	for _, d := range BuiltinDefinitions() {
		if d.Name == name {
			return d, true
		}
	}
	return ProviderDefinition{}, false
}

// EnvAPIKey resolves a provider's API key from the environment only —
// its declared EnvAPIKeyVars first, then the generic LLM_API_KEY. No
// vault read, so it is safe during bootstrap before the vault opens.
// Unknown / local providers fall straight through to LLM_API_KEY.
func EnvAPIKey(name string) string {
	if d, ok := Builtin(name); ok {
		for _, v := range d.EnvAPIKeyVars {
			if k := os.Getenv(v); k != "" {
				return k
			}
		}
	}
	return os.Getenv("LLM_API_KEY")
}

// defaultGeminiModel is the shared default for both Google surfaces —
// the Gemini API and Vertex AI serve the same model family.
const defaultGeminiModel = "gemini-2.5-flash"

// --- Built-in provider definitions ---

// BuiltinDefinitions returns the hard-coded seed set. The registry
// layers DB rows on top during Reload.
func BuiltinDefinitions() []ProviderDefinition {
	return []ProviderDefinition{
		{
			Name:          "openai",
			DisplayName:   "OpenAI",
			AdapterType:   AdapterTypes.OPENAICOMPATIBLE,
			BaseURL:       "https://api.openai.com/v1",
			DefaultModel:  "gpt-4o-mini",
			SeedModels:    []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "o1", "o1-mini", "o3-mini"},
			EnvAPIKeyVars: []string{"OPENAI_API_KEY"},
			Builtin:       true,
			Enabled:       true,
		},
		{
			Name:          "deepseek",
			DisplayName:   "DeepSeek",
			AdapterType:   AdapterTypes.DEEPSEEKCOMPATIBLE,
			BaseURL:       deepseek.DefaultBaseURL,
			BaseURLEnv:    "DEEPSEEK_BASE_URL",
			DefaultModel:  "deepseek-chat",
			SeedModels:    []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-chat", "deepseek-reasoner"},
			EnvAPIKeyVars: []string{"DEEPSEEK_API_KEY"},
			Builtin:       true,
			Enabled:       true,
		},
		{
			Name:          DefaultBuiltinName.String(),
			DisplayName:   "llama.cpp",
			AdapterType:   AdapterTypes.OPENAICOMPATIBLE,
			BaseURL:       llamacpp.DefaultBaseURL,
			BaseURLEnv:    "LLAMACPP_BASE_URL",
			DefaultModel:  "",
			SeedModels:    nil,
			EnvAPIKeyVars: nil,
			Builtin:       true,
			Enabled:       true,
		},
		{
			Name:          NameOllama.String(),
			DisplayName:   "Ollama",
			AdapterType:   AdapterTypes.OPENAICOMPATIBLE,
			BaseURL:       ollama.DefaultBaseURL,
			BaseURLEnv:    "OLLAMA_BASE_URL",
			DefaultModel:  "",
			SeedModels:    nil,
			EnvAPIKeyVars: nil,
			Builtin:       true,
			Enabled:       true,
		},
		{
			Name:         "anthropic",
			DisplayName:  "Anthropic",
			AdapterType:  AdapterTypes.ANTHROPICCOMPATIBLE,
			BaseURL:      "",
			DefaultModel: "claude-sonnet-4-6",
			SeedModels: []string{
				"claude-opus-4-7", "claude-opus-4-6", "claude-opus-4-5",
				"claude-sonnet-4-6", "claude-sonnet-4-5",
				"claude-haiku-4-5",
			},
			EnvAPIKeyVars: []string{"ANTHROPIC_API_KEY"},
			Builtin:       true,
			Enabled:       true,
		},
		{
			Name:         "gemini",
			DisplayName:  "Google Gemini",
			AdapterType:  AdapterTypes.GOOGLECOMPATIBLE,
			BaseURL:      "",
			DefaultModel: defaultGeminiModel,
			SeedModels: []string{
				"gemini-2.5-pro", defaultGeminiModel, "gemini-2.5-flash-lite",
				"gemini-2.0-pro", "gemini-2.0-flash", "gemini-2.0-flash-lite",
				"gemini-1.5-pro", "gemini-1.5-flash",
			},
			EnvAPIKeyVars: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
			Builtin:       true,
			Enabled:       true,
		},
		{
			Name:         "google-vertex",
			DisplayName:  "Google Vertex AI",
			AdapterType:  AdapterTypes.GOOGLEVERTEX,
			BaseURL:      "",
			DefaultModel: defaultGeminiModel,
			SeedModels: []string{
				"gemini-2.5-pro", defaultGeminiModel, "gemini-2.5-flash-lite",
				"gemini-2.0-pro", "gemini-2.0-flash", "gemini-2.0-flash-lite",
			},
			// No API key at all: Vertex authenticates via Application
			// Default Credentials, with project/location from the
			// GOOGLE_CLOUD_PROJECT / GOOGLE_CLOUD_LOCATION environment
			// (the GCP-native convention — anyone using Vertex has them).
			EnvAPIKeyVars: nil,
			Builtin:       true,
			Enabled:       true,
		},
		{
			Name:         "openai-codex",
			DisplayName:  "OpenAI Codex (OAuth)",
			AdapterType:  AdapterTypes.OAUTHOPENAICODEX,
			BaseURL:      "",
			DefaultModel: "",
			// The Codex backend does expose /codex/models for OAuth-authenticated
			// clients, but the generic registry has no OAuth TokenSource. The
			// picker falls back to these presets; zarlcode's Settings layer uses
			// openaicodex.FetchContextWindow for the live context-window cap.
			SeedModels:    openAICodexSeedModelIDs(),
			EnvAPIKeyVars: nil, // OAuth-backed — key resolution is different
			Builtin:       true,
			Enabled:       true,
		},
		{
			Name:         "claude-code",
			DisplayName:  "Claude Code (OAuth)",
			AdapterType:  AdapterTypes.OAUTHCLAUDECODE,
			BaseURL:      "",
			DefaultModel: "",
			// OAuth-backed; the generic registry has no TokenSource, so surface
			// the package's preset catalogue or the picker shows an empty list.
			SeedModels:    claudecode.ListPresetModels(),
			EnvAPIKeyVars: nil, // OAuth-backed — key resolution is different
			Builtin:       true,
			Enabled:       true,
		},
	}
}

// openAICodexSeedModelIDs flattens the codex preset catalogue to plain
// model ids for SeedModels. openaicodex.ListPresetModels returns rich
// llm.Model entries; the picker only needs the ids.
func openAICodexSeedModelIDs() []string {
	presets := openaicodex.ListPresetModels()
	ids := make([]string, 0, len(presets))
	for _, p := range presets {
		ids = append(ids, p.ID)
	}
	return ids
}

// --- Adapter registration (called at init) ---

func init() {
	// OpenAI-compatible adapters (static API key)
	registerAdapter(openAICompatible, adapterDef{
		build: func(p buildParams) (llm.Provider, error) {
			opts := []options.Option[openai.Provider]{
				openai.WithReasoningHistory(p.reasoningHistory),
			}
			if p.baseURL != "" {
				opts = append(opts, openai.WithBaseURL(p.baseURL))
			}
			if p.model != "" {
				opts = append(opts, openai.WithModel(p.model))
			}
			if p.cachePrompt {
				opts = append(opts, openai.WithCachePrompt(true))
			}
			return openai.NewProvider(p.apiKey, opts...)
		},
		noKeyOK: false,
	})

	// DeepSeek-compatible. deepseek.NewProvider returns its own facade type
	// (*deepseek.Provider) — it auto-selects reasoning_content handling from
	// the model (V4 echoes it, R1 strips it), so it is NOT a plain
	// *openai.Provider and must be registered under its own concrete type.
	registerAdapter(deepSeekCompatible, adapterDef{
		build: func(p buildParams) (llm.Provider, error) {
			opts := []options.Option[deepseek.Provider]{}
			if p.baseURL != "" {
				opts = append(opts, deepseek.WithBaseURL(p.baseURL))
			}
			if p.model != "" {
				opts = append(opts, deepseek.WithModel(p.model))
			}
			return deepseek.NewProvider(p.apiKey, opts...)
		},
		noKeyOK: false,
	})

	// Anthropic-compatible
	registerAdapter(anthropicCompatible, adapterDef{
		build: func(p buildParams) (llm.Provider, error) {
			opts := []options.Option[anthropic.Provider]{}
			if p.baseURL != "" {
				opts = append(opts, anthropic.WithBaseURL(p.baseURL))
			}
			if p.model != "" {
				opts = append(opts, anthropic.WithModel(p.model))
			}
			return anthropic.NewProvider(p.apiKey, opts...)
		},
		noKeyOK: false,
	})

	// Google-compatible (Gemini API)
	registerAdapter(googleCompatible, adapterDef{
		build: func(p buildParams) (llm.Provider, error) {
			opts := []options.Option[google.Provider]{}
			if p.model != "" {
				opts = append(opts, google.WithModel(p.model))
			}
			return google.NewProvider(p.apiKey, opts...)
		},
		noKeyOK: false,
	})

	// Google Vertex AI: ADC-authenticated, no API key in play — the
	// adapter ignores p.apiKey (including the registry's keyless
	// placeholder) and resolves project/location from p.options or
	// the SDK's environment lookup.
	registerAdapter(googleVertex, adapterDef{
		build: func(p buildParams) (llm.Provider, error) {
			opts := []options.Option[google.Provider]{}
			if p.model != "" {
				opts = append(opts, google.WithModel(p.model))
			}
			if p.baseURL != "" {
				opts = append(opts, google.WithBaseURL(p.baseURL))
			}
			project, _ := p.options["project"].(string)
			location, _ := p.options["location"].(string)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return google.NewVertexProvider(ctx, project, location, opts...)
		},
		noKeyOK: true,
	})
}
