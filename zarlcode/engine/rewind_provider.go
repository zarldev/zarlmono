package engine

import (
	"context"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// PrepareRewindProvider reconstructs a saved route without consulting today's
// cache alias row. The returned spec keeps original intent for later switches;
// facts describe only this construction, not a restored budget or rate card.
func (s *Settings) PrepareRewindProvider(ctx context.Context, saved rewind.Target) (TargetUpdate, error) {
	intent, err := saved.CacheIntent.RoutePolicy()
	if err != nil {
		return TargetUpdate{}, err
	}
	forced, err := saved.CachePin.RoutePolicy()
	if err != nil {
		return TargetUpdate{}, err
	}
	if forced == (backends.RouteCachePolicy{}) {
		forced = backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}
	}
	spec := ProviderSpec{Name: saved.Provider, Model: saved.Model, CodexEffort: saved.CodexEffort, CachePolicy: intent}
	built, err := buildProviderResolved(ctx, s.Registry, s.Svc, spec, forced)
	if err != nil {
		return TargetUpdate{}, err
	}
	update := TargetUpdate{Provider: built.Provider, Spec: spec}
	if saved.Provider == "anthropic" {
		update.Facts = &TargetFacts{CachePolicy: built.CachePolicy}
	}
	return update, nil
}
