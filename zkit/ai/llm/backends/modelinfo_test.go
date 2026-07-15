package backends_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

func newModelInfoRegistry() *backends.ProviderRegistry {
	return backends.NewRegistry(backends.WithStore(newFakeStore()), backends.WithSettingsService(fakeKeyService{}))
}

func TestRegistryCost(t *testing.T) {
	reg := newModelInfoRegistry()

	if in, out, ok := reg.Cost("anthropic", "claude-opus-4-7"); !ok || in != 0.015 || out != 0.075 {
		t.Errorf("anthropic opus cost = %v/%v ok=%v, want 0.015/0.075 true", in, out, ok)
	}
	if in, _, ok := reg.Cost("gemini", "gemini-2.5-pro"); !ok || in != 0.00125 {
		t.Errorf("gemini 2.5-pro input cost = %v ok=%v, want 0.00125 true", in, ok)
	}
	if _, _, ok := reg.Cost("openai", "gpt-4o-mini"); !ok {
		t.Error("openai gpt-4o-mini should be metered")
	}
	// Local + subscription backends are not metered per token.
	if _, _, ok := reg.Cost("llamacpp", "qwen3"); ok {
		t.Error("llamacpp (local) must not be metered")
	}
	if _, _, ok := reg.Cost("openai-codex", "gpt-5.5"); ok {
		t.Error("openai-codex (subscription) must not be metered")
	}
	if _, _, ok := reg.Cost("claude-code", "opus"); ok {
		t.Error("claude-code (subscription) must not be metered")
	}
}

func TestRegistryCapabilities(t *testing.T) {
	reg := newModelInfoRegistry()

	if !reg.Capabilities("anthropic", "claude-opus-4-7").SupportsThinking {
		t.Error("claude opus 4.x should support thinking")
	}
	if !reg.Capabilities("gemini", "gemini-2.5-pro").SupportsVision {
		t.Error("gemini should support vision")
	}
	if reg.Capabilities("deepseek", "deepseek-chat").SupportsVision {
		t.Error("deepseek is text-only — no vision")
	}
}

func TestRegistryProviderClass(t *testing.T) {
	reg := newModelInfoRegistry()

	if !reg.IsLocal("llamacpp") || !reg.IsLocal("ollama") {
		t.Error("llamacpp/ollama should be local")
	}
	if reg.IsLocal("openai") {
		t.Error("openai is not local")
	}
	if !reg.IsSubscription("openai-codex") || !reg.IsSubscription("claude-code") {
		t.Error("codex / claude-code should be subscription")
	}
	if reg.IsSubscription("anthropic") {
		t.Error("anthropic API is metered, not subscription")
	}
}

// stubModelsDevServer returns an httptest server serving a minimal
// models.dev-shaped JSON response.
func stubModelsDevServer(t *testing.T) *httptest.Server {
	t.Helper()
	payload := map[string]any{
		"openai": map[string]any{
			"models": []map[string]any{
				{
					"id":         "gpt-4o",
					"tool_call":  true,
					"reasoning":  false,
					"modalities": map[string]any{"input": []string{"text", "image"}, "output": []string{"text"}},
					"cost":       map[string]any{"input": 5.0, "output": 15.0},
				},
				{
					"id":         "o3",
					"tool_call":  true,
					"reasoning":  true,
					"modalities": map[string]any{"input": []string{"text", "image"}, "output": []string{"text"}},
					"cost":       map[string]any{"input": 2.0, "output": 8.0},
				},
			},
		},
		"deepseek": map[string]any{
			"models": []map[string]any{
				{
					"id":         "deepseek-chat",
					"tool_call":  true,
					"reasoning":  false,
					"modalities": map[string]any{"input": []string{"text"}, "output": []string{"text"}},
					"cost":       map[string]any{"input": 0.14, "output": 0.28},
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRegistryResolveCost_FallbackToStatic(t *testing.T) {
	reg := newModelInfoRegistry()

	in, out, ok := reg.ResolveCost(t.Context(), "anthropic", "claude-sonnet-4-6")
	if !ok || in != 0.003 || out != 0.015 {
		t.Errorf("ResolveCost(anthropic, claude-sonnet-4-6) = %v/%v ok=%v, want 0.003/0.015 true", in, out, ok)
	}

	// Static fallback for known metered model.
	if _, _, ok := reg.ResolveCost(t.Context(), "openai", "gpt-4o-mini"); !ok {
		t.Error("ResolveCost(openai, gpt-4o-mini) should be metered via static fallback")
	}
}

func TestRegistryResolveCost_ModelsDevSource(t *testing.T) {
	srv := stubModelsDevServer(t)
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	src := modelsdev.New(store, modelsdev.WithBaseURL(srv.URL), modelsdev.WithTTL(0))
	reg := backends.NewRegistry(
		backends.WithStore(newFakeStore()),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(src),
	)

	// models.dev gives per-1M; resolve divides by 1000 → per-1k.
	in, out, ok := reg.ResolveCost(t.Context(), "openai", "gpt-4o")
	if !ok || in != 0.005 || out != 0.015 {
		t.Errorf("ResolveCost(openai, gpt-4o) = %v/%v ok=%v, want 0.005/0.015 true", in, out, ok)
	}

	// Local backends are not metered.
	if _, _, ok := reg.ResolveCost(t.Context(), "llamacpp", "qwen3"); ok {
		t.Error("ResolveCost(llamacpp, qwen3) must not be metered (local)")
	}
}

func TestRegistryResolveCapabilities_ModelsDevSource(t *testing.T) {
	srv := stubModelsDevServer(t)
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	src := modelsdev.New(store, modelsdev.WithBaseURL(srv.URL), modelsdev.WithTTL(0))
	reg := backends.NewRegistry(
		backends.WithStore(newFakeStore()),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(src),
	)

	// gpt-4o: vision from models.dev, no thinking.
	caps := reg.ResolveCapabilities(t.Context(), "openai", "gpt-4o")
	if !caps.SupportsVision {
		t.Error("gpt-4o should support vision")
	}
	if caps.SupportsThinking {
		t.Error("gpt-4o should not support thinking")
	}

	// o3: reasoning model.
	caps = reg.ResolveCapabilities(t.Context(), "openai", "o3")
	if !caps.SupportsThinking {
		t.Error("o3 should support thinking")
	}

	// deepseek-chat: text-only, falls back to static.
	caps = reg.ResolveCapabilities(t.Context(), "deepseek", "deepseek-chat")
	if caps.SupportsVision {
		t.Error("deepseek-chat should not support vision")
	}
}

// TestResolveCost_ExplicitOverrideWins verifies that an explicit per-provider
// cost override (InputCostPerMTok > 0) wins over both models.dev and static rates.
func TestResolveCost_ExplicitOverrideWins(t *testing.T) {
	srv := stubModelsDevServer(t) // openai/gpt-4o at $5/$15 per million
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	src := modelsdev.New(store, modelsdev.WithBaseURL(srv.URL), modelsdev.WithTTL(0))

	fstore := newFakeStore()
	reg := backends.NewRegistry(
		backends.WithStore(fstore),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(src),
	)

	// Add a custom provider with explicit cost override ($10/$30 per million).
	err := reg.UpsertProvider(t.Context(), backends.ProviderDefinition{
		Name:              "my-override-provider",
		DisplayName:       "Override Provider",
		AdapterType:       backends.AdapterTypes.OPENAICOMPATIBLE,
		BaseURL:           "http://localhost:8080",
		DefaultModel:      "gpt-4o",
		InputCostPerMTok:  10.0,
		OutputCostPerMTok: 30.0,
		Enabled:           true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Explicit cost should win over models.dev's $5/$15 and static $2.5/$10.
	in, out, ok := reg.ResolveCost(t.Context(), "my-override-provider", "gpt-4o")
	if !ok || in != 0.010 || out != 0.030 {
		t.Errorf("ResolveCost(my-override-provider, gpt-4o) = %v/%v ok=%v, want 0.010/0.030 true", in, out, ok)
	}
}

// TestResolveCost_CrossProviderNoLeak verifies that the same model ID under
// two different providers resolves independently — costs from one provider
// do not leak to the other.
func TestResolveCost_CrossProviderNoLeak(t *testing.T) {
	srv := stubModelsDevServer(t) // openai/gpt-4o at $5/$15 per million
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	src := modelsdev.New(store, modelsdev.WithBaseURL(srv.URL), modelsdev.WithTTL(0))

	fstore := newFakeStore()
	reg := backends.NewRegistry(
		backends.WithStore(fstore),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(src),
	)

	// Add a custom openai-compatible provider with explicit cost override.
	err := reg.UpsertProvider(t.Context(), backends.ProviderDefinition{
		Name:              "custom-openai",
		DisplayName:       "Custom OpenAI",
		AdapterType:       backends.AdapterTypes.OPENAICOMPATIBLE,
		BaseURL:           "http://localhost:9090",
		DefaultModel:      "gpt-4o",
		InputCostPerMTok:  10.0,
		OutputCostPerMTok: 30.0,
		Enabled:           true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Built-in "openai" provider: models.dev wins for gpt-4o ($5/$15).
	in, out, ok := reg.ResolveCost(t.Context(), "openai", "gpt-4o")
	if !ok || in != 0.005 || out != 0.015 {
		t.Errorf("openai gpt-4o: got %v/%v ok=%v, want 0.005/0.015 true", in, out, ok)
	}

	// Custom provider with same model: explicit override ($10/$30) is independent.
	in, out, ok = reg.ResolveCost(t.Context(), "custom-openai", "gpt-4o")
	if !ok || in != 0.010 || out != 0.030 {
		t.Errorf("custom-openai gpt-4o: got %v/%v ok=%v, want 0.010/0.030 true", in, out, ok)
	}
}

// TestResolveCost_LocalUnmetered verifies that local providers (llamacpp/ollama)
// return ok=false even when the model name exists in models.dev data.
func TestResolveCost_LocalUnmetered(t *testing.T) {
	srv := stubModelsDevServer(t) // has gpt-4o in its data
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	src := modelsdev.New(store, modelsdev.WithBaseURL(srv.URL), modelsdev.WithTTL(0))

	reg := backends.NewRegistry(
		backends.WithStore(newFakeStore()),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(src),
	)

	for _, name := range []string{"llamacpp", "ollama"} {
		in, out, ok := reg.ResolveCost(t.Context(), name, "gpt-4o")
		if ok {
			t.Errorf("ResolveCost(%q, gpt-4o) unexpectedly metered: %v/%v", name, in, out)
		}
	}
}

// TestResolveCost_SubscriptionUnmetered verifies that OAuth subscription
// providers (openai-codex, claude-code) return ok=false even when the model
// name exists in models.dev data or the static table.
func TestResolveCost_SubscriptionUnmetered(t *testing.T) {
	srv := stubModelsDevServer(t)
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	src := modelsdev.New(store, modelsdev.WithBaseURL(srv.URL), modelsdev.WithTTL(0))

	reg := backends.NewRegistry(
		backends.WithStore(newFakeStore()),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(src),
	)

	for _, tc := range []struct{ name, model string }{
		{"openai-codex", "gpt-4o"},
		{"claude-code", "claude-sonnet-4-6"},
	} {
		in, out, ok := reg.ResolveCost(t.Context(), tc.name, tc.model)
		if ok {
			t.Errorf("ResolveCost(%q, %q) unexpectedly metered: %v/%v (subscription)", tc.name, tc.model, in, out)
		}
	}
}

// TestResolveCost_UnknownNoZeroPrice verifies that unknown models or providers
// return ok=false rather than a zero-valued metered price.
func TestResolveCost_UnknownNoZeroPrice(t *testing.T) {
	reg := newModelInfoRegistry()

	// Unknown model for a known metered provider.
	in, out, ok := reg.ResolveCost(t.Context(), "openai", "completely-unknown-model-v99")
	if ok {
		t.Errorf("ResolveCost(openai, unknown-model) unexpectedly metered: %v/%v", in, out)
	}

	// Unknown provider.
	in, out, ok = reg.ResolveCost(t.Context(), "nonexistent-provider", "gpt-4o")
	if ok {
		t.Errorf("ResolveCost(nonexistent, gpt-4o) unexpectedly metered: %v/%v", in, out)
	}
}

// TestResolveCost_FetchFailureFallback verifies that when models.dev fetch fails
// and there's no cached snapshot, ResolveCost falls back to the static table
// without error.
func TestResolveCost_FetchFailureFallback(t *testing.T) {
	// Server that returns HTTP 500 to simulate a failed fetch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	src := modelsdev.New(store, modelsdev.WithBaseURL(srv.URL), modelsdev.WithTTL(0))

	reg := backends.NewRegistry(
		backends.WithStore(newFakeStore()),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(src),
	)

	// Static fallback for known metered model despite failed fetch.
	in, out, ok := reg.ResolveCost(t.Context(), "openai", "gpt-4o-mini")
	if !ok || in != 0.00015 || out != 0.0006 {
		t.Errorf("ResolveCost(openai, gpt-4o-mini) after fetch failure = %v/%v ok=%v, want 0.00015/0.0006 true", in, out, ok)
	}
}

// TestResolveCapabilities_PartialModelsDevEntry verifies that a models.dev
// entry with only cost/context data and no capability fields (or all false
// booleans) does NOT suppress known static capabilities.
func TestResolveCapabilities_PartialModelsDevEntry(t *testing.T) {
	// Stub server serving gpt-4o with cost data but tool_call=false
	// and no vision modalities — a partial entry.
	payload := map[string]any{
		"openai": map[string]any{
			"models": []map[string]any{
				{
					"id":         "gpt-4o",
					"tool_call":  false,
					"reasoning":  false,
					"modalities": map[string]any{"input": []string{"text"}, "output": []string{"text"}},
					"limit":      map[string]any{"context": 128000, "output": 16384},
					"cost":       map[string]any{"input": 5.0, "output": 15.0},
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)

	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	src := modelsdev.New(store, modelsdev.WithBaseURL(srv.URL), modelsdev.WithTTL(0))

	reg := backends.NewRegistry(
		backends.WithStore(newFakeStore()),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(src),
	)

	caps := reg.ResolveCapabilities(t.Context(), "openai", "gpt-4o")

	// Static table says gpt-4o supports tools and vision.
	// Partial models.dev entry must not override these to false.
	if !caps.SupportsTools {
		t.Error("gpt-4o should support tools (from static table, not suppressed by partial models.dev)")
	}
	if !caps.SupportsVision {
		t.Error("gpt-4o should support vision (from static table, not suppressed by partial models.dev)")
	}
	// Static table says no thinking for gpt-4o (not a reasoning model family).
	if caps.SupportsThinking {
		t.Error("gpt-4o should not support thinking")
	}
	// Streaming and system are always true.
	if !caps.SupportsStreaming {
		t.Error("gpt-4o should support streaming")
	}
	if !caps.SupportsSystem {
		t.Error("gpt-4o should support system")
	}
}

func TestEstimateCost_ProviderModelBoundEstimate(t *testing.T) {
	reg := newModelInfoRegistry()

	est, ok := reg.EstimateCost(t.Context(), "openai", "gpt-4o-mini", llm.Usage{
		PromptTokens:     2000,
		CompletionTokens: 1000,
	})
	if !ok {
		t.Fatal("EstimateCost ok=false, want true")
	}
	if est.InputUSD != 0.0003 || est.OutputUSD != 0.0006 || est.TotalUSD != 0.0009 {
		t.Fatalf("EstimateCost = %+v, want input 0.0003 output 0.0006 total 0.0009", est)
	}
	if est.Incomplete {
		t.Fatalf("Incomplete = true, want false: %+v", est)
	}
}

func TestEstimateCost_UnknownOrCachedIncomplete(t *testing.T) {
	reg := newModelInfoRegistry()

	if est, ok := reg.EstimateCost(t.Context(), "llamacpp", "gpt-4o-mini", llm.Usage{PromptTokens: 1000}); ok || !est.Incomplete {
		t.Fatalf("local EstimateCost = %+v ok=%v, want incomplete false-ok", est, ok)
	}

	est, ok := reg.EstimateCost(t.Context(), "openai", "gpt-4o-mini", llm.Usage{
		PromptTokens:     1000,
		CompletionTokens: 1000,
		CachedTokens:     500,
	})
	if !ok {
		t.Fatal("EstimateCost cached ok=false, want true")
	}
	if !est.Incomplete || est.Reason != "cached_token_discount_unknown" {
		t.Fatalf("cached EstimateCost = %+v, want incomplete cached_token_discount_unknown", est)
	}
}
