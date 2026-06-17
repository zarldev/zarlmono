package backends

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// buildParams is the internal interchange type the registry uses to pass
// resolved overrides to an adapter. It is NOT a public config struct —
// consumers use BuildConfig, which the registry resolves into this before
// calling the adapter closure.
type buildParams struct {
	apiKey           string
	baseURL          string
	model            string
	reasoningHistory llm.ReasoningHistory
	options          llm.ModelOptions
	// cachePrompt requests the llama.cpp-only `cache_prompt` extension on
	// the built provider. Set only for the llama.cpp built-in; strict
	// OpenAI-compatible backends and forwarding proxies (LiteLLM) reject
	// the unknown field with HTTP 400.
	cachePrompt bool
}

// adapterDef bundles the constructor for one adapter type.
type adapterDef struct {
	build func(buildParams) (llm.Provider, error)
	// noKeyOK reports whether the adapter works without an API key (e.g.
	// llamacpp, ollama). When false, the registry requires successful
	// key resolution before Build proceeds.
	noKeyOK bool
}

// adapterRegistry maps each adapterType constant to the adapterDef that knows
// how to build it. Populated by init-time registration from definitions.go.
var adapterRegistry = map[adapterType]adapterDef{}

// registerAdapter stores def in the registry keyed by at.
func registerAdapter(at adapterType, def adapterDef) {
	adapterRegistry[at] = def
}

// lookupAdapter returns the adapterDef for at, or false when not found.
func lookupAdapter(at adapterType) (adapterDef, bool) {
	d, ok := adapterRegistry[at]
	return d, ok
}
