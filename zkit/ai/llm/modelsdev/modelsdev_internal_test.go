package modelsdev

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/cache"
)

// stubAPI returns an httptest server serving a minimal models.dev-shaped
// JSON response for the tests.
func stubAPI(t *testing.T) *httptest.Server {
	t.Helper()
	payload := map[string]any{
		"openai": map[string]any{
			"models": []map[string]any{
				{
					"id":         "gpt-4o",
					"tool_call":  true,
					"reasoning":  false,
					"modalities": map[string]any{"input": []string{"text", "image"}, "output": []string{"text"}},
					"limit":      map[string]any{"context": 128000, "output": 16384},
					"cost":       map[string]any{"input": 5.0, "output": 15.0},
				},
				{
					"id":         "o3",
					"tool_call":  true,
					"reasoning":  true,
					"modalities": map[string]any{"input": []string{"text", "image"}, "output": []string{"text"}},
					"limit":      map[string]any{"context": 200000, "output": 100000},
					"cost":       map[string]any{"input": 2.0, "output": 8.0},
				},
			},
		},
		"anthropic": map[string]any{
			"models": []map[string]any{
				{
					"id":         "claude-sonnet-4-6",
					"tool_call":  true,
					"reasoning":  true,
					"modalities": map[string]any{"input": []string{"text", "image"}, "output": []string{"text"}},
					"limit":      map[string]any{"context": 200000, "output": 64000},
					"cost":       map[string]any{"input": 3.0, "output": 15.0},
				},
			},
		},
		"google": map[string]any{
			"models": []map[string]any{
				{
					"id":         "gemini-2.5-pro",
					"tool_call":  true,
					"reasoning":  true,
					"modalities": map[string]any{"input": []string{"text", "image", "video"}, "output": []string{"text"}},
					"limit":      map[string]any{"context": 1048576, "output": 65536},
					"cost":       map[string]any{"input": 1.25, "output": 10.0},
				},
				{
					"id":         "gemini-2.5-flash",
					"tool_call":  true,
					"reasoning":  false,
					"modalities": map[string]any{"input": []string{"text"}, "output": []string{"text"}},
					"limit":      map[string]any{"context": 1048576, "output": 65536},
					"cost":       map[string]any{"input": 0.3, "output": 2.5},
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
					"limit":      map[string]any{"context": 131072, "output": 32768},
					"cost":       map[string]any{"input": 0.14, "output": 0.28},
				},
				{
					"id":         "deepseek-reasoner",
					"tool_call":  true,
					"reasoning":  true,
					"modalities": map[string]any{"input": []string{"text"}, "output": []string{"text"}},
					"limit":      map[string]any{"context": 131072, "output": 32768},
					"cost":       map[string]any{"input": 0.55, "output": 2.19},
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

func TestSource_Lookup(t *testing.T) {
	srv := stubAPI(t)
	store := cache.NewMemoryCache[string, Snapshot]()
	src := New(store, WithBaseURL(srv.URL), WithTTL(0))

	// OpenAI — gpt-4o: vision=true, thinking=false
	e, ok := src.Lookup(t.Context(), "openai", "gpt-4o")
	if !ok {
		t.Fatal("gpt-4o not found")
	}
	if e.InputCostPerMTok != 5.0 || e.OutputCostPerMTok != 15.0 {
		t.Errorf("cost = %v/%v, want 5/15", e.InputCostPerMTok, e.OutputCostPerMTok)
	}
	if e.ContextWindow != 128000 {
		t.Errorf("context_window = %d, want 128000", e.ContextWindow)
	}
	if e.MaxOutputTokens != 16384 {
		t.Errorf("max_output = %d, want 16384", e.MaxOutputTokens)
	}
	if !e.SupportsTools || !e.SupportsVision || e.SupportsVideo || e.SupportsThinking {
		t.Errorf("capabilities: tools=%v vision=%v video=%v thinking=%v, want true/true/false/false",
			e.SupportsTools, e.SupportsVision, e.SupportsVideo, e.SupportsThinking)
	}

	// OpenAI — o3: reasoning model
	e, ok = src.Lookup(t.Context(), "openai", "o3")
	if !ok {
		t.Fatal("o3 not found")
	}
	if !e.SupportsThinking {
		t.Error("o3 should support thinking")
	}

	// Anthropic
	e, ok = src.Lookup(t.Context(), "anthropic", "claude-sonnet-4-6")
	if !ok {
		t.Fatal("claude-sonnet-4-6 not found")
	}
	if e.InputCostPerMTok != 3.0 || e.OutputCostPerMTok != 15.0 {
		t.Errorf("cost = %v/%v, want 3/15", e.InputCostPerMTok, e.OutputCostPerMTok)
	}
	if !e.SupportsThinking {
		t.Error("claude-sonnet-4-6 should support thinking")
	}

	// Google via alias "gemini"
	e, ok = src.Lookup(t.Context(), "gemini", "gemini-2.5-pro")
	if !ok {
		t.Fatal("gemini-2.5-pro not found via 'gemini' alias")
	}
	if e.InputCostPerMTok != 1.25 {
		t.Errorf("cost input = %v, want 1.25", e.InputCostPerMTok)
	}
	if e.ContextWindow != 1048576 {
		t.Errorf("context_window = %d, want 1048576", e.ContextWindow)
	}
	if !e.SupportsVision {
		t.Error("gemini-2.5-pro should support vision")
	}
	if !e.SupportsVideo {
		t.Error("gemini-2.5-pro should support video")
	}

	// Google via alias "google-vertex"
	e, ok = src.Lookup(t.Context(), "google-vertex", "gemini-2.5-flash")
	if !ok {
		t.Fatal("gemini-2.5-flash not found via 'google-vertex' alias")
	}
	if e.SupportsVision || e.SupportsVideo || e.SupportsThinking {
		t.Error("gemini-2.5-flash should not have vision, video, or thinking")
	}

	// DeepSeek
	e, ok = src.Lookup(t.Context(), "deepseek", "deepseek-reasoner")
	if !ok {
		t.Fatal("deepseek-reasoner not found")
	}
	if !e.SupportsThinking {
		t.Error("deepseek-reasoner should support thinking")
	}
	if e.SupportsVision {
		t.Error("deepseek-reasoner should not support vision")
	}
	if e.SupportsVideo {
		t.Error("deepseek-reasoner should not support video")
	}

	// Unknown model
	_, ok = src.Lookup(t.Context(), "openai", "nonexistent-model")
	if ok {
		t.Error("nonexistent model should not be found")
	}

	// Unknown provider
	_, ok = src.Lookup(t.Context(), "llamacpp", "qwen3")
	if ok {
		t.Error("llamacpp (local) should not be found in models.dev")
	}
}

func TestSource_CacheReuse(t *testing.T) {
	srv := stubAPI(t)
	store := cache.NewMemoryCache[string, Snapshot]()
	// TTL large enough that the first fetch stays valid.
	src := New(store, WithBaseURL(srv.URL), WithTTL(100*365*24*3600*1e9))

	// First call populates the cache.
	_, ok := src.Lookup(t.Context(), "openai", "gpt-4o")
	if !ok {
		t.Fatal("first lookup failed")
	}
	// Verify the cache key was written.
	_, err := store.Get(t.Context(), snapshotKey)
	if err != nil {
		t.Fatalf("cache miss after first fetch: %v", err)
	}

	// Second call — cache hit, no network round-trip needed.
	_, ok = src.Lookup(t.Context(), "anthropic", "claude-sonnet-4-6")
	if !ok {
		t.Fatal("second lookup (cache hit) failed")
	}
}

func TestProviderAlias(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"openai", "openai"},
		{"anthropic", "anthropic"},
		{"deepseek", "deepseek"},
		{"gemini", "google"},
		{"google-vertex", "google"},
		{"google", "google"},
		{"llamacpp", "llamacpp"},
		{"ollama", "ollama"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := providerAlias(tc.name)
			if got != tc.want {
				t.Errorf("providerAlias(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
