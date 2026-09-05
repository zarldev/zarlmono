package openai

import (
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// CostPer1k returns the published USD per-1k-token (input, output) rate for
// an OpenAI model, matched by family (specific variants first). ok=false for
// ids without a known rate, so the caller shows "unknown rate" rather than a
// wrong number. Rates are approximate and drift as OpenAI re-prices.
func CostPer1k(model string) (float64, float64, bool) {
	m := strings.ToLower(model)
	switch {
	case m == modelGPT6Astra:
		return 0.010, 0.050, true
	case strings.Contains(m, modelGPT56Sol) || m == modelGPT56:
		return 0.005, 0.030, true
	case strings.Contains(m, modelGPT56Terra):
		return 0.0025, 0.015, true
	case strings.Contains(m, modelGPT56Luna):
		return 0.001, 0.006, true
	case strings.Contains(m, "gpt-4o-mini"):
		return 0.00015, 0.0006, true
	case strings.Contains(m, "gpt-4o"):
		return 0.0025, 0.010, true
	case strings.Contains(m, "gpt-4.1-mini"), strings.Contains(m, "gpt-4.1-nano"):
		return 0.0004, 0.0016, true
	case strings.Contains(m, "gpt-4.1"):
		return 0.002, 0.008, true
	case strings.Contains(m, "o4-mini"), strings.Contains(m, "o3-mini"), strings.Contains(m, "o1-mini"):
		return 0.0011, 0.0044, true
	case strings.Contains(m, "o3"):
		return 0.002, 0.008, true
	case strings.Contains(m, "o1"):
		return 0.015, 0.060, true
	}
	return 0, 0, false
}

const gpt6AstraLongContextThreshold = 272_000

// CostPer1kForPrompt returns the effective per-1k rates for one request after
// applying model-specific prompt-length pricing tiers.
func CostPer1kForPrompt(model string, promptTokens int) (float64, float64, bool) {
	input, output, ok := CostPer1k(model)
	if !ok {
		return 0, 0, false
	}
	input, output = AdjustCostPer1kForPrompt(model, promptTokens, input, output)
	return input, output, true
}

// AdjustCostPer1kForPrompt applies OpenAI's model-specific prompt-length tiers
// to already-resolved base rates. GPT-6 Astra requests over 272K input tokens
// charge 2x input and 1.5x output for the complete request.
func AdjustCostPer1kForPrompt(model string, promptTokens int, input, output float64) (float64, float64) {
	if strings.ToLower(model) == modelGPT6Astra && promptTokens > gpt6AstraLongContextThreshold {
		return input * 2, output * 1.5
	}
	return input, output
}

// Capabilities reports what an OpenAI-compatible model supports. The
// OPENAICOMPATIBLE adapter also serves local servers (llama.cpp / Ollama),
// so unknown ids get a conservative baseline (streaming/tools/system, no
// vision/thinking) rather than a false positive.
func Capabilities(model string) llm.ModelCapabilities {
	m := strings.ToLower(model)
	reasoning := strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4") || strings.Contains(m, "gpt-5") || m == modelGPT6Astra
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision: reasoning || strings.Contains(m, "gpt-4o") ||
			strings.Contains(m, "gpt-4.1"),
		SupportsVideo:    reasoning || strings.Contains(m, "gpt-4o"),
		SupportsThinking: reasoning,
	}
}
