package backends_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

func TestDocumentedRouteContextBeatsGenericCatalog(t *testing.T) {
	t.Setenv("CLAUDE_CODE_DISABLE_1M_CONTEXT", "")
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"google":{"models":[{"id":"gemini-2.5-pro","limit":{"context":2097152}}]},"anthropic":{"models":[{"id":"claude-sonnet-4-6","limit":{"context":1000000}}]}}`)
	}))
	t.Cleanup(server.Close)
	source := modelsdev.New(cache.NewMemoryCache[string, modelsdev.Snapshot](), modelsdev.WithBaseURL(server.URL))
	// Ensure cached and network-backed resolution see a real conflicting entry.
	registry := backends.NewRegistry(backends.WithModelsDevSource(source))
	_ = registry.ResolveContextWindow(t.Context(), "anthropic", "", "claude-sonnet-4-6")
	for _, tc := range []struct {
		route, model string
		want         int
	}{{"gemini", "gemini-2.5-pro", 1048576}, {"google-vertex", "gemini-2.5-pro", 1048576}, {"claude-code", "claude-sonnet-4-6", 200000}} {
		if got := registry.ResolveContextWindow(t.Context(), tc.route, "", tc.model); got != tc.want {
			t.Fatalf("live %s = %d, want %d", tc.route, got, tc.want)
		}
		if got := registry.ResolveContextWindowCached(tc.route, tc.model); got != tc.want {
			t.Fatalf("cached %s = %d, want %d", tc.route, got, tc.want)
		}
	}
}
