package engine_test

import (
	"testing"
	"time"

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

	modelCache := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	source := modelsdev.New(modelCache)
	if err := modelCache.Set(t.Context(), "models.dev", modelsdev.Snapshot{
		Schema:    2,
		FetchedAt: time.Now(),
		Entries: map[string]map[string]modelsdev.Entry{
			"openai": {
				"metadata-only-model": {
					Intrinsic: modelsdev.Intrinsic{
						ContextWindow:    123_456,
						SupportsTools:    true,
						SupportsThinking: true,
					},
					Pricing: modelsdev.Pricing{InputCostPerMTok: 2, OutputCostPerMTok: 8},
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed models.dev cache: %v", err)
	}

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
