package anthropic_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestContextWindowFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model string
		want  int
	}{
		// Explicit contemporary native windows, not family-wide guesses.
		{"claude-opus-4-7", 1_000_000},
		{"claude-fable-5-1", 1_000_000},
		{"claude-opus-5-5", 1_000_000},
		{"claude-sonnet-5-5", 1_000_000},
		{"claude-haiku-5-5", 1_000_000},
		{"claude-opus-4-6", 1_000_000},
		{"claude-opus-4-5", 200_000},
		{"claude-sonnet-4-6", 1_000_000},
		{"claude-sonnet-4-5", 200_000},
		{"claude-haiku-4-5", 200_000},

		// Known picker aliases normalize to native 1M API identifiers.
		{"claude-opus-4-7[1m]", 1_000_000},
		{"claude-sonnet-4-6[1m]", 1_000_000},
		{"claude-opus-4-7-1m", 1_000_000},
		{"not-a-model[1m]", 0},
		{"claude-sonnet-4-5-1m", 0},

		// Claude 3.x: 200k
		{"claude-3-7-sonnet-latest", 200_000},
		{"claude-3-5-sonnet-20241022", 200_000},
		{"claude-3-5-haiku-latest", 200_000},
		{"claude-3-opus-latest", 200_000},
		{"claude-3-haiku-20240307", 200_000},

		// Empty / unknown: 0 — caller's fallback chain decides
		{"", 0},
		{"future-claude-id", 0},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			t.Parallel()
			if got := anthropic.ContextWindowFor(c.model); got != c.want {
				t.Errorf("ContextWindowFor(%q) = %d, want %d", c.model, got, c.want)
			}
		})
	}
}
