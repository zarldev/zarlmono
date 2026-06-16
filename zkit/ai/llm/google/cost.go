package google

import (
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// CostPer1k returns the published USD per-1k-token (input, output) rate for a
// Gemini model, matched by family (pro / flash, by generation). ok=false for
// unknown ids. Rates are approximate and drift as Google re-prices.
func CostPer1k(model string) (float64, float64, bool) {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "2.5-pro"):
		return 0.00125, 0.010, true
	case strings.Contains(m, "2.5-flash"):
		return 0.0003, 0.0025, true
	case strings.Contains(m, "1.5-pro"):
		return 0.00125, 0.005, true
	case strings.Contains(m, "1.5-flash"):
		return 0.000075, 0.0003, true
	case strings.Contains(m, "gemini"):
		return 0.0003, 0.0025, true
	}
	return 0, 0, false
}

// Capabilities reports what a Gemini model supports. Gemini is natively
// multimodal; the 2.x line exposes thinking.
func Capabilities(model string) llm.ModelCapabilities {
	m := strings.ToLower(model)
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision:    true,
		SupportsThinking:  strings.Contains(m, "2.5") || strings.Contains(m, "thinking"),
	}
}
