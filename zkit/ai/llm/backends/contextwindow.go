package backends

import (
	"context"
	"os"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

// geminiContextWindow is the published window shared by all current
// Gemini 1.5 / 2.x chat models.
const geminiContextWindow = 1_048_576

// ContextWindow returns the known hard context window (tokens) for a
// (provider, model) pair. 0 means "unknown — probe the server or keep
// the current value"; local backends (llamacpp/ollama) and custom
// providers fall here. Resolution dispatches on the provider's adapter
// type to the owning package's per-model table.
func (r *ProviderRegistry) ContextWindow(name, model string) int {
	def, err := r.Parse(name)
	if err != nil {
		return 0
	}
	// An explicit per-provider window (custom providers whose models aren't in
	// any static table) takes precedence over the adapter's per-model table.
	if def.ContextWindow > 0 {
		return def.ContextWindow
	}
	return contextWindowForAdapter(def.AdapterType, model)
}

// ResolveContextWindowCached resolves a hosted model's window from explicit
// configuration, the process-local models.dev snapshot, then static tables. It
// never probes local servers, reads disk, or performs network I/O.
func (r *ProviderRegistry) ResolveContextWindowCached(name, model string) int {
	def, err := r.Parse(name)
	if err == nil && def.ContextWindow > 0 {
		return def.ContextWindow
	}
	if r.modelsDevSource != nil {
		if entry, ok := r.modelsDevSource.LookupCached(name, model); ok && entry.ContextWindow > 0 {
			return entry.ContextWindow
		}
	}
	if window := r.ContextWindow(name, model); window > 0 {
		return window
	}
	if r.modelsDevSource != nil {
		if intrinsic, ok := r.modelsDevSource.LookupIntrinsicCached(model); ok && intrinsic.ContextWindow > 0 {
			return intrinsic.ContextWindow
		}
	}
	return 0
}

// ResolveContextWindow returns the usable context window (tokens) for a
// (provider, model), the way each provider actually reports it:
//
//   - Hosted providers (Anthropic, OpenAI, Codex, Gemini, DeepSeek, …)
//     publish a fixed per-model window with no query API, so those come
//     from the static [ProviderRegistry.ContextWindow] table. Codex OAuth is
//     the exception at the zarlcode Settings layer: it can ask /codex/models
//     with the vault-backed TokenSource for the backend's effective cap.
//   - Local servers (llama.cpp, Ollama) are launched with a window that
//     varies per deployment, so those are probed over HTTP — llama.cpp via
//     /props, Ollama via /api/show.
//
// baseURL is the endpoint the provider was built against (empty → the
// provider's own default). Returns 0 when undeterminable, so the caller can
// keep its current default rather than show a wrong number.
func (r *ProviderRegistry) ResolveContextWindow(ctx context.Context, name, baseURL, model string) int {
	def, err := r.Parse(name)
	if err == nil && def.ContextWindow > 0 {
		return def.ContextWindow
	}
	// A local server's configured runtime limit is stronger than the model's
	// theoretical catalogue limit. Probe it before metadata/static fallbacks.
	if id, parseErr := llm.ParseLLMProvider(name); parseErr == nil {
		switch id {
		case DefaultBuiltinName:
			if cw := llamacpp.ProbeContextWindow(ctx, baseURL); cw > 0 {
				return cw
			}
		case NameOllama:
			if cw := ollama.ContextWindowFor(ctx, baseURL, model); cw > 0 {
				return cw
			}
		}
	}
	if r.modelsDevSource != nil {
		if e, ok := r.modelsDevSource.Lookup(ctx, name, model); ok && e.ContextWindow > 0 {
			return e.ContextWindow
		}
	}
	if cw := r.ContextWindow(name, model); cw > 0 {
		return cw
	}
	// Custom/local/OpenAI-compatible endpoints often expose model IDs owned by
	// another provider. Use provider-independent intrinsic metadata only when
	// all normalized catalogue matches agree.
	if r.modelsDevSource != nil {
		if e, ok := r.modelsDevSource.LookupIntrinsic(ctx, model); ok && e.ContextWindow > 0 {
			return e.ContextWindow
		}
	}
	return 0
}

func contextWindowForAdapter(at AdapterType, model string) int {
	switch at {
	case AdapterTypes.OPENAICOMPATIBLE:
		// openai's table; unknown ids (local qwen, custom models) → 0.
		return openai.ContextWindowFor(model)
	case AdapterTypes.DEEPSEEKCOMPATIBLE:
		return deepseek.ContextWindowFor(model)
	case AdapterTypes.ANTHROPICCOMPATIBLE:
		return anthropic.ContextWindowFor(model)
	case AdapterTypes.GOOGLECOMPATIBLE:
		return geminiContextWindow
	case AdapterTypes.OAUTHOPENAICODEX:
		return openaicodex.ContextWindowFor(model)
	case AdapterTypes.OAUTHCLAUDECODE:
		return claudecode.ContextWindowFor(model)
	default:
		return 0
	}
}

// ResolveBaseURL returns the URL a consumer should thread into a
// BuildConfig (and the picker should fetch models from), applying
// the same precedence the old backends.Backend.BaseURL did:
//
//  1. def.BaseURLEnv (e.g. LLAMACPP_BASE_URL) when set.
//  2. activeURL when activeName matches name (user pointed LLM_BASE_URL
//     at the active provider).
//  3. def.BaseURL (the static default; "" for SDK-managed endpoints).
//
// Unknown providers resolve to "".
func (r *ProviderRegistry) ResolveBaseURL(name, activeName, activeURL string) string {
	def, err := r.Parse(name)
	if err != nil {
		return ""
	}
	if def.BaseURLEnv != "" {
		if v := os.Getenv(def.BaseURLEnv); v != "" {
			return v
		}
	}
	if activeName == name && activeURL != "" {
		return activeURL
	}
	return def.BaseURL
}
