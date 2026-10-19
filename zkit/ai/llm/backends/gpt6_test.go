package backends_test

import (
	"math"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestGPT6SolLunaRegistryMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		model         string
		input, output float64
	}{
		{"gpt-6.1-sol", 0.002, 0.010},
		{"gpt-6-sol", 0.002, 0.010},
		{"gpt-6-luna", 0.0001, 0.0005},
	} {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			reg := newModelInfoRegistry()
			for _, provider := range []string{"openai", "openai-codex"} {
				def, ok := backends.Builtin(provider)
				if !ok || !slices.Contains(def.SeedModels, tc.model) {
					t.Fatalf("%s seed missing %s", provider, tc.model)
				}
				if got := reg.ContextWindow(provider, tc.model); got != 1_050_000 {
					t.Fatalf("%s context = %d", provider, got)
				}
				caps := reg.Capabilities(provider, tc.model)
				if !caps.SupportsVision || !caps.SupportsThinking || !caps.SupportsTools || caps.SupportsVideo {
					t.Fatalf("%s capabilities = %+v", provider, caps)
				}
			}
			for _, tokens := range []int{272000, 272001} {
				in, out := tc.input, tc.output
				if tokens > 272000 {
					in *= 2
					out *= 1.5
				}
				usage := llm.Usage{PromptTokens: tokens, CompletionTokens: 1000}
				est, ok := reg.EstimateCost(t.Context(), "openai", tc.model, usage)
				wantIn := float64(tokens) / 1000 * in
				if !ok || est.Incomplete || math.Abs(est.InputUSD-wantIn) > 1e-12 || math.Abs(est.OutputUSD-out) > 1e-12 || math.Abs(est.TotalUSD-wantIn-out) > 1e-12 {
					t.Fatalf("API estimate at %d = %+v (%v)", tokens, est, ok)
				}
				usage.CachedTokens = 100
				est, ok = reg.EstimateCost(t.Context(), "openai", tc.model, usage)
				if !ok || !est.Incomplete || est.Reason != "cached_token_discount_unknown" {
					t.Fatalf("cache estimate must remain incomplete: %+v (%v)", est, ok)
				}
				if _, ok := reg.EstimateCost(t.Context(), "openai-codex", tc.model, usage); ok {
					t.Fatal("Codex subscription must not inherit API token billing")
				}
			}
			if err := reg.UpsertProvider(t.Context(), backends.ProviderDefinition{
				Name: "private-openai", DisplayName: "Private OpenAI", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
				BaseURL: "http://localhost:8080", DefaultModel: tc.model, Enabled: true,
				InputCostPerMTok: 10, OutputCostPerMTok: 30,
			}); err != nil {
				t.Fatal(err)
			}
			in, out, ok := reg.ResolveCostForPrompt(t.Context(), "private-openai", tc.model, 272001)
			if !ok || in != 0.010 || out != 0.030 {
				t.Fatalf("explicit private rates changed: %g/%g (%v)", in, out, ok)
			}
		})
	}
}
