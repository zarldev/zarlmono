package openai_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

// Pinning the per-model context window table — the historical "default
// 128k for unknown" behaviour silently overstated 8k/16k/32k models,
// so each major family is asserted here so a future refactor that
// drops the table can't regress those values into a single fallback.
func TestContextWindowFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model string
		want  int
	}{
		// GPT-4.1 family: 1M (newest hosted line)
		{"gpt-4.1", 1_000_000},
		{"gpt-4.1-mini", 1_000_000},
		{"gpt-4.1-nano", 1_000_000},

		// o1 / o3: 200k for full reasoning models, 128k for mini/preview
		{"o1", 200_000},
		{"o1-mini", 128_000},
		{"o1-preview", 128_000},
		{"o3", 200_000},
		{"o3-mini", 200_000},

		// GPT-4o / 4-turbo: 128k
		{"gpt-4o", 128_000},
		{"gpt-4o-mini", 128_000},
		{"gpt-4-turbo", 128_000},
		{"gpt-4-turbo-preview", 128_000},

		// Legacy GPT-4: 8k / 32k — these were the silently-overstated
		// ones under the old default-128k fallback.
		{"gpt-4", 8_192},
		{"gpt-4-0613", 8_192},
		{"gpt-4-32k", 32_768},
		{"gpt-4-32k-0613", 32_768},

		// GPT-3.5: 16k / 4k
		{"gpt-3.5-turbo", 16_385},
		{"gpt-3.5-turbo-0125", 16_385},
		{"gpt-3.5-turbo-16k", 16_385},
		{"gpt-3.5-turbo-0613", 4_096},
		{"gpt-3.5-turbo-instruct", 4_096},

		// GPT-5: hosted OpenAI API windows
		{"gpt-5.6", 1_050_000},
		{"gpt-5.6-sol", 1_050_000},
		{"gpt-5.6-terra", 1_050_000},
		{"gpt-5.6-luna", 1_050_000},
		{"gpt-5.5", 1_000_000},
		{"gpt-5.4", 400_000},
		{"gpt-5.4-mini", 400_000},

		// Empty / unknown: 0 (caller's fallback chain decides)
		{"", 0},
		{"some-future-model", 0},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			t.Parallel()
			if got := openai.ContextWindowFor(c.model); got != c.want {
				t.Errorf("ContextWindowFor(%q) = %d, want %d", c.model, got, c.want)
			}
		})
	}
}
