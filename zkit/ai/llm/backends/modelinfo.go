package backends

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
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

func adjustCostForPrompt(at AdapterType, model string, promptTokens int, input, output float64) (float64, float64) {
	if at == AdapterTypes.OPENAICOMPATIBLE {
		return openai.AdjustCostPer1kForPrompt(model, promptTokens, input, output)
	}
	return input, output
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

// ResolveCostForPrompt resolves the effective per-1k rates for one request,
// including provider-specific prompt-length pricing tiers. Explicit provider
// price overrides remain authoritative and are not multiplied.
func (r *ProviderRegistry) ResolveCostForPrompt(ctx context.Context, name, model string, promptTokens int) (float64, float64, bool) {
	if r.IsLocal(name) {
		return 0, 0, false
	}
	def, err := r.Parse(name)
	if err != nil {
		return 0, 0, false
	}
	if def.InputCostPerMTok > 0 || def.OutputCostPerMTok > 0 {
		return def.InputCostPerMTok / 1000, def.OutputCostPerMTok / 1000, true
	}
	if r.modelsDevSource != nil {
		if entry, ok := r.modelsDevSource.Lookup(ctx, name, model); ok && (entry.InputCostPerMTok > 0 || entry.OutputCostPerMTok > 0) {
			input, output := entry.InputCostPerMTok/1000, entry.OutputCostPerMTok/1000
			input, output = adjustCostForPrompt(def.AdapterType, model, promptTokens, input, output)
			return input, output, true
		}
	}
	input, output, ok := costForAdapter(def.AdapterType, model)
	if !ok {
		return 0, 0, false
	}
	input, output = adjustCostForPrompt(def.AdapterType, model, promptTokens, input, output)
	return input, output, true
}

// ResolveCostCached resolves pricing from explicit overrides, the process-local
// models.dev snapshot, then static tables. It never performs I/O and is safe on
// render paths.
func (r *ProviderRegistry) ResolveCostCached(name, model string) (float64, float64, bool) {
	if r.IsLocal(name) {
		return 0, 0, false
	}
	def, err := r.Parse(name)
	if err != nil {
		return 0, 0, false
	}
	if def.InputCostPerMTok > 0 || def.OutputCostPerMTok > 0 {
		return def.InputCostPerMTok / 1000, def.OutputCostPerMTok / 1000, true
	}
	if r.modelsDevSource != nil {
		if entry, ok := r.modelsDevSource.LookupCached(name, model); ok && (entry.InputCostPerMTok > 0 || entry.OutputCostPerMTok > 0) {
			return entry.InputCostPerMTok / 1000, entry.OutputCostPerMTok / 1000, true
		}
	}
	return costForAdapter(def.AdapterType, model)
}

// ResolveCapabilities consults models.dev before falling back to the
// static per-package table. Unknown providers/models return the zero
// value. Unlike ResolveCost, local providers are not short-circuited:
// they still have capabilities (vision, thinking) — only cost is zero
// for unmetered providers.
//
// Merge semantics: static table capabilities are the authoritative base.
// Models.dev non-zero fields overlay on top — zero/absent models.dev
// fields are never interpreted as an explicit "unsupported" claim, because
// the snapshot can be stale or incomplete.
func (r *ProviderRegistry) ResolveCapabilities(ctx context.Context, name, model string) llm.ModelCapabilities {
	def, err := r.Parse(name)
	if err != nil {
		return llm.ModelCapabilities{}
	}
	caps := capabilitiesForAdapter(def.AdapterType, model)
	if r.modelsDevSource != nil {
		entry, ok := r.modelsDevSource.Lookup(ctx, name, model)
		if !ok {
			entry.Intrinsic, ok = r.modelsDevSource.LookupIntrinsic(ctx, model)
		}
		if ok {
			mergeCapabilities(&caps, entry.Intrinsic)
		}
	}
	// Streaming and system support are near-universal for hosted providers
	// and not tracked by models.dev.
	caps.SupportsStreaming = true
	caps.SupportsSystem = true
	return caps
}

// ResolveCapabilitiesCached merges static capabilities with the process-local
// models.dev snapshot. It never performs I/O and is safe on render paths.
func (r *ProviderRegistry) ResolveCapabilitiesCached(name, model string) llm.ModelCapabilities {
	def, err := r.Parse(name)
	if err != nil {
		return llm.ModelCapabilities{}
	}
	caps := capabilitiesForAdapter(def.AdapterType, model)
	if r.modelsDevSource != nil {
		entry, ok := r.modelsDevSource.LookupCached(name, model)
		if !ok {
			entry.Intrinsic, ok = r.modelsDevSource.LookupIntrinsicCached(model)
		}
		if ok {
			mergeCapabilities(&caps, entry.Intrinsic)
		}
	}
	caps.SupportsStreaming = true
	caps.SupportsSystem = true
	return caps
}

func mergeCapabilities(caps *llm.ModelCapabilities, intrinsic modelsdev.Intrinsic) {
	if intrinsic.SupportsTools {
		caps.SupportsTools = true
	}
	if intrinsic.SupportsThinking {
		caps.SupportsThinking = true
	}
	if intrinsic.SupportsVision {
		caps.SupportsVision = true
	}
	if intrinsic.SupportsVideo {
		caps.SupportsVideo = true
	}
}

// CostEstimate is an estimated token cost at listed provider/model rates. It is
// not an invoice amount: provider-specific cached-token discounts, multimodal
// charges, tool charges, taxes, and contract pricing are not represented by the
// rate metadata this package resolves.
type CostEstimate struct {
	InputUSD   float64
	OutputUSD  float64
	TotalUSD   float64
	Incomplete bool
	Reason     string
}

// EstimateCost estimates the cost for one request using effective provider/model
// rates, including prompt-length tiers. ok=false means the provider/model has no
// metered token rate known to the registry (local, subscription, unknown, or
// unavailable).
func (r *ProviderRegistry) EstimateCost(ctx context.Context, provider, model string, usage llm.Usage) (CostEstimate, bool) {
	inPer1K, outPer1K, ok := r.ResolveCostForPrompt(ctx, provider, model, usage.PromptTokens)
	if !ok {
		return CostEstimate{Incomplete: true, Reason: "unknown_or_unmetered"}, false
	}
	inputUSD := float64(usage.PromptTokens) / 1000 * inPer1K
	outputUSD := float64(usage.CompletionTokens) / 1000 * outPer1K
	est := CostEstimate{
		InputUSD:   inputUSD,
		OutputUSD:  outputUSD,
		TotalUSD:   inputUSD + outputUSD,
		Incomplete: usage.CachedTokens > 0,
	}
	if est.Incomplete {
		est.Reason = "cached_token_discount_unknown"
	}
	return est, true
}
