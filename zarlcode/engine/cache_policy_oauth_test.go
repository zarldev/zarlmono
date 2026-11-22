package engine_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestOAuthConstructionRejectsMalformedCacheIntentBeforeCredentials(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"openai-codex", "claude-code"} {
		for _, policy := range []backends.RouteCachePolicy{
			{Mode: backends.RouteCacheModes.CACHEDEFAULT, System: true},
			{Mode: backends.RouteCacheModes.CACHEINHERIT, RollingTTL: backends.RouteCacheTTLs.CACHETTL1H},
		} {
			_, err := engine.BuildProvider(t.Context(), nil, nil, engine.ProviderSpec{Name: provider, CachePolicy: policy})
			if !errors.Is(err, backends.ErrInvalidRouteCachePolicy) {
				t.Fatalf("%s admitted invalid cache shape before credential resolution: %v", provider, err)
			}
		}
	}
}
