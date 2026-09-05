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
