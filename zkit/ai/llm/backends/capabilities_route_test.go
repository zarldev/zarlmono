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

func TestClaudeCodeRouteCapabilitiesConstrainIntrinsicMetadata(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"anthropic": map[string]any{
			"models": map[string]any{
				"claude-sonnet-4-6": map[string]any{
					"id":         "claude-sonnet-4-6",
					"tool_call":  true,
					"reasoning":  true,
					"modalities": map[string]any{"input": []string{"text", "image", "video"}, "output": []string{"text"}},
				},
			},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("encode fixture: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	source := modelsdev.New(cache.NewMemoryCache[string, modelsdev.Snapshot](), modelsdev.WithBaseURL(server.URL), modelsdev.WithTTL(0))
	registry := backends.NewRegistry(
		backends.WithStore(newFakeStore()),
		backends.WithSettingsService(fakeKeyService{}),
		backends.WithModelsDevSource(source),
	)

	resolved := registry.ResolveCapabilities(t.Context(), "claude-code", "claude-sonnet-4-6")
	assertClaudeCodeTextOnly(t, resolved.SupportsVision, resolved.SupportsVideo)
	cached := registry.ResolveCapabilitiesCached("claude-code", "claude-sonnet-4-6")
	assertClaudeCodeTextOnly(t, cached.SupportsVision, cached.SupportsVideo)
}

func assertClaudeCodeTextOnly(t *testing.T, vision, video bool) {
	t.Helper()
	if vision || video {
		t.Fatalf("Claude Code route capabilities: vision=%v video=%v, want both false", vision, video)
	}
}
