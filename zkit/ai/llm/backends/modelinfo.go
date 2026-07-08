package backends

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

// Cost returns the per-1k USD (input, output) rate for a (provider, model).
// ok=false when the backend isn't metered per token — local servers
// (llama.cpp / Ollama), flat subscriptions (Codex / Claude Code), and unknown
// rates all fall here, so a consumer renders "no metered cost" rather than a
// wrong number. Resolution dispatches on the provider's adapter type to the
// owning package's per-model table, mirroring ContextWindow.
func (r *ProviderRegistry) Cost(name, model string) (float64, float64, bool) {
	if r.IsLocal(name) {
		return 0, 0, false
	}
	def, err := r.Parse(name)
	if err != nil {
		return 0, 0, false
	}
	// An explicit per-provider price (USD per 1M tokens, custom providers not
	// in any static table) wins. Cost reports per-1k, so divide by 1000.
	if def.InputCostPerMTok > 0 || def.OutputCostPerMTok > 0 {
		return def.InputCostPerMTok / 1000, def.OutputCostPerMTok / 1000, true
	}
	return costForAdapter(def.AdapterType, model)
}

func costForAdapter(at AdapterType, model string) (float64, float64, bool) {
	switch at {
	case AdapterTypes.OPENAICOMPATIBLE:
		return openai.CostPer1k(model)
	case AdapterTypes.DEEPSEEKCOMPATIBLE:
		return deepseek.CostPer1k(model)
	case AdapterTypes.ANTHROPICCOMPATIBLE:
		return anthropic.CostPer1k(model)
	case AdapterTypes.GOOGLECOMPATIBLE:
		return google.CostPer1k(model)
	default:
		// OAUTH adapters (Codex / Claude Code) bill via subscription.
		return 0, 0, false
	}
}

// Capabilities reports what a (provider, model) supports — used to gate UI
// affordances (a thinking toggle, image attach) on what the model can
// actually do. Unknown providers/models return the zero value (nothing
// claimed).
func (r *ProviderRegistry) Capabilities(name, model string) llm.ModelCapabilities {
	def, err := r.Parse(name)
	if err != nil {
		return llm.ModelCapabilities{}
	}
	return capabilitiesForAdapter(def.AdapterType, model)
}

func capabilitiesForAdapter(at AdapterType, model string) llm.ModelCapabilities {
	switch at {
	case AdapterTypes.OPENAICOMPATIBLE:
		return openai.Capabilities(model)
	case AdapterTypes.DEEPSEEKCOMPATIBLE:
		return deepseek.Capabilities(model)
	case AdapterTypes.ANTHROPICCOMPATIBLE:
		return anthropic.Capabilities(model)
	case AdapterTypes.GOOGLECOMPATIBLE:
		return google.Capabilities(model)
	case AdapterTypes.OAUTHOPENAICODEX:
		return openaicodex.Capabilities(model)
	case AdapterTypes.OAUTHCLAUDECODE:
		return claudecode.Capabilities(model)
	default:
		return llm.ModelCapabilities{}
	}
}

// IsLocal reports whether the provider is a local, unmetered server
// (llama.cpp / Ollama) — no per-token cost. Centralises the knowledge that
// used to live as a name-literal switch in the zarlcode cockpit.
func (r *ProviderRegistry) IsLocal(name string) bool {
	id, err := llm.ParseLLMProvider(name)
	if err != nil {
		return false
	}
	return id == DefaultBuiltinName || id == NameOllama
}

// IsSubscription reports whether the provider bills via a flat subscription
// (ChatGPT Codex / Claude Code) rather than per-token metering, derived from
// the registry's adapter type rather than a name literal.
func (r *ProviderRegistry) IsSubscription(name string) bool {
	def, err := r.Parse(name)
	if err != nil {
		return false
	}
	return def.AdapterType == AdapterTypes.OAUTHOPENAICODEX ||
		def.AdapterType == AdapterTypes.OAUTHCLAUDECODE
}

// ResolveCost returns the per-1k USD (input, output) rate, consulting
// models.dev between the per-provider DB override and the static
// per-package table. Keeps the same ok contract as Cost.
func (r *ProviderRegistry) ResolveCost(ctx context.Context, name, model string) (float64, float64, bool) {
	if r.IsLocal(name) {
		return 0, 0, false
	}
	def, err := r.Parse(name)
	if err != nil {
		return 0, 0, false
	}
	// 1. Explicit per-provider DB override wins.
	if def.InputCostPerMTok > 0 || def.OutputCostPerMTok > 0 {
		return def.InputCostPerMTok / 1000, def.OutputCostPerMTok / 1000, true
	}
	// 2. Live models.dev lookup.
	if r.modelsDevSource != nil {
		if e, ok := r.modelsDevSource.Lookup(ctx, name, model); ok && (e.InputCostPerMTok > 0 || e.OutputCostPerMTok > 0) {
			return e.InputCostPerMTok / 1000, e.OutputCostPerMTok / 1000, true
		}
	}
	// 3. Static per-package table.
	return costForAdapter(def.AdapterType, model)
}

// ResolveCapabilities consults models.dev before falling back to the
// static per-package table. Unknown providers/models return the zero
// value. Unlike ResolveCost, local providers are not short-circuited:
// they still have capabilities (vision, thinking) — only cost is zero
// for unmetered providers.
func (r *ProviderRegistry) ResolveCapabilities(ctx context.Context, name, model string) llm.ModelCapabilities {
	def, err := r.Parse(name)
	if err != nil {
		return llm.ModelCapabilities{}
	}
	// 1. Live models.dev lookup.
	if r.modelsDevSource != nil {
		if e, ok := r.modelsDevSource.Lookup(ctx, name, model); ok {
			return llm.ModelCapabilities{
				SupportsTools:    e.SupportsTools,
				SupportsThinking: e.SupportsThinking,
				SupportsVision:   e.SupportsVision,
				SupportsVideo:    e.SupportsVideo,
				// models.dev doesn't track streaming/system support —
				// those are near-universal for hosted providers.
				SupportsStreaming: true,
				SupportsSystem:    true,
			}
		}
	}
	// 2. Static per-package table.
	return capabilitiesForAdapter(def.AdapterType, model)
}
