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

// Capabilities reports what an OpenAI-compatible model supports. The
// OPENAICOMPATIBLE adapter also serves local servers (llama.cpp / Ollama),
// so unknown ids get a conservative baseline (streaming/tools/system, no
// vision/thinking) rather than a false positive.
func Capabilities(model string) llm.ModelCapabilities {
	m := strings.ToLower(model)
	reasoning := strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4") || strings.Contains(m, "gpt-5")
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision: reasoning || strings.Contains(m, "gpt-4o") ||
			strings.Contains(m, "gpt-4.1"),
		SupportsThinking: reasoning,
	}
}
