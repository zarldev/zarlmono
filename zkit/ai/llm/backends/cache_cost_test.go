package backends_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestEstimateCostCachePricingCoverage(t *testing.T) {
	registry := newModelInfoRegistry()
	tests := []struct {
		name       string
		usage      llm.Usage
		incomplete bool
		reason     string
	}{
		{name: "uncached", usage: llm.Usage{PromptTokens: 1000}},
		{
			name:       "cache reads unpriced",
			usage:      llm.Usage{PromptTokens: 1000, CachedTokens: 500},
			incomplete: true,
			reason:     "cached_token_discount_unknown",
		},
		{
			name:       "cache writes unpriced",
			usage:      llm.Usage{PromptTokens: 1000, CacheCreationTokens: 500, CacheCreation5mTokens: 500},
			incomplete: true,
			reason:     "cache_write_pricing_unknown",
		},
		{
			name:       "cache reads and writes unpriced",
			usage:      llm.Usage{PromptTokens: 1000, CachedTokens: 250, CacheCreationTokens: 500, CacheCreation1hTokens: 500},
			incomplete: true,
			reason:     "cache_write_pricing_unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimate, ok := registry.EstimateCost(t.Context(), "openai", "gpt-4o-mini", tt.usage)
			if !ok {
				t.Fatal("EstimateCost ok=false, want true")
			}
			if estimate.Incomplete != tt.incomplete || estimate.Reason != tt.reason {
				t.Fatalf("EstimateCost = %+v, want incomplete=%v reason=%q", estimate, tt.incomplete, tt.reason)
			}
		})
	}
}
