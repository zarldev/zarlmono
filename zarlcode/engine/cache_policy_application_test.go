package engine_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

type cacheRows map[string]backends.RouteCachePolicy

func (r cacheRows) GetRouteCachePolicy(_ context.Context, _, alias string) (backends.RouteCachePolicy, error) {
	p, ok := r[alias]
	if !ok {
		return p, backends.ErrRouteCachePolicyNotFound
	}
	return p, nil
}
func (r cacheRows) SetRouteCachePolicy(_ context.Context, _, alias string, p backends.RouteCachePolicy) error {
	r[alias] = p
	return nil
}
func (r cacheRows) DeleteRouteCachePolicy(_ context.Context, _, alias string) error {
	delete(r, alias)
	return nil
}

type cacheKey struct{}

func (cacheKey) GetKey(context.Context, string) (string, error) { return "test-key", nil }

func TestActualCacheFactsAndRewindPin(t *testing.T) {
	rows := cacheRows{"first": {Mode: backends.RouteCacheModes.CACHEDISABLED}}
	settings := &engine.Settings{Registry: backends.NewRegistry(backends.WithRouteCachePolicyStore(rows), backends.WithSettingsService(cacheKey{}))}
	first, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "anthropic", Model: "first", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Facts.CachePolicy.Mode != backends.RouteCacheModes.CACHEDISABLED || first.Spec.CachePolicy.Mode != backends.RouteCacheModes.CACHEINHERIT {
		t.Fatalf("actual/cache intent: %+v %+v", first.Facts.CachePolicy, first.Spec.CachePolicy)
	}
	saved := engine.CheckpointTarget(engine.RunTarget{Spec: first.Spec, Model: "first", Facts: first.Facts})
	if saved.CachePin.Mode != "disabled" || saved.CacheIntent != (rewind.CachePolicy{}) {
		t.Fatalf("saved policy: %+v", saved)
	}
	rows["first"] = backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL5M, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}
	rows["second"] = backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL5M, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}
	// Rebuild using a transient pin, while original intent remains inherited.
	restored, err := settings.PrepareRewindProvider(t.Context(), saved)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Facts.CachePolicy.Mode != backends.RouteCacheModes.CACHEDISABLED || restored.Spec.CachePolicy.Mode != backends.RouteCacheModes.CACHEINHERIT {
		t.Fatalf("restored policy: %+v %+v", restored.Facts.CachePolicy, restored.Spec.CachePolicy)
	}
	destination := restored.Spec
	destination.Model, destination.APIKey = "second", "test-key"
	dest, err := settings.PrepareTargetCached(t.Context(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if dest.Facts.CachePolicy.StaticTTL != backends.RouteCacheTTLs.CACHETTL5M {
		t.Fatalf("destination policy: %+v", dest.Facts.CachePolicy)
	}
	masked, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "anthropic", Model: "first", APIKey: "test-key", CachePolicy: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}})
	if err != nil {
		t.Fatal(err)
	}
	if masked.Facts.CachePolicy.StaticTTL != backends.RouteCacheTTLs.CACHETTL1H || masked.Spec.CachePolicy.Mode != backends.RouteCacheModes.CACHEDEFAULT {
		t.Fatalf("mask: %+v %+v", masked.Facts.CachePolicy, masked.Spec.CachePolicy)
	}
}
