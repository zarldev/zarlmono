package engine_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSettingsUsesModelsDevMetadata(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"openai":{"models":{"metadata-only-model":{"id":"metadata-only-model","tool_call":true,"reasoning":true,"limit":{"context":123456},"cost":{"input":2,"output":8}}}}}`)
	}))
	t.Cleanup(server.Close)
	source := modelsdev.New(cache.NewMemoryCache[string, modelsdev.Snapshot](), modelsdev.WithBaseURL(server.URL))

	settings := engine.NewSettings(store, nil, source, t.TempDir())
	spec := engine.ProviderSpec{Name: "openai", Model: "metadata-only-model"}
	if got := settings.ContextWindow(t.Context(), spec); got != 123_456 {
		t.Fatalf("context window = %d, want 123456", got)
	}
	caps := settings.Registry.ResolveCapabilities(t.Context(), spec.Name, spec.Model)
	if !caps.SupportsTools || !caps.SupportsThinking {
		t.Fatalf("capabilities = %+v, want tools and thinking", caps)
	}
	input, output, ok := settings.Registry.ResolveCost(t.Context(), spec.Name, spec.Model)
	if !ok || input != 0.002 || output != 0.008 {
		t.Fatalf("cost = %v/%v ok=%v, want 0.002/0.008 true", input, output, ok)
	}
}
