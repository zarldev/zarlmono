package openai_test

import (
	"math"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestCostPer1kForPromptGPT6AstraLongContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		promptTokens int
		wantInput    float64
		wantOutput   float64
	}{
		{name: "at threshold", promptTokens: 272_000, wantInput: 0.010, wantOutput: 0.050},
		{name: "over threshold", promptTokens: 272_001, wantInput: 0.020, wantOutput: 0.075},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, output, ok := openai.CostPer1kForPrompt("gpt-6-astra", tt.promptTokens)
			if !ok || math.Abs(input-tt.wantInput) > 1e-12 || math.Abs(output-tt.wantOutput) > 1e-12 {
				t.Fatalf("CostPer1kForPrompt() = %v/%v ok=%v, want %v/%v true", input, output, ok, tt.wantInput, tt.wantOutput)
			}
		})
	}
}

func TestGPT6SolLunaPricesAndCapabilities(t *testing.T) {
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
			for _, tokens := range []int{1000, 272000, 272001} {
				wantIn, wantOut := tc.input, tc.output
				if tokens > 272000 {
					wantIn *= 2
					wantOut *= 1.5
				}
				in, out, ok := openai.CostPer1kForPrompt(tc.model, tokens)
				if !ok || math.Abs(in-wantIn) > 1e-12 || math.Abs(out-wantOut) > 1e-12 {
					t.Fatalf("price at %d = %g/%g (%v), want %g/%g", tokens, in, out, ok, wantIn, wantOut)
				}
			}
			caps := openai.Capabilities(tc.model)
			if !caps.SupportsThinking || !caps.SupportsVision || !caps.SupportsTools || !caps.SupportsStreaming || !caps.SupportsSystem || caps.SupportsVideo {
				t.Fatalf("capabilities = %+v; want text/image reasoning and tools, not video", caps)
			}
		})
	}
	if _, _, ok := openai.CostPer1k("gpt-6-unknown"); ok {
		t.Fatal("unknown GPT-6 price must not be inferred")
	}
}
