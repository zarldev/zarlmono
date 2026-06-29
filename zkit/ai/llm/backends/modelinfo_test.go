package backends_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
