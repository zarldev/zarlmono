package deepseek

import (
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// CostPer1k returns the published USD per-1k-token (input, output) rate for a
// DeepSeek model (input is the cache-miss rate). v4-pro is the post-promo
// tier; the flash / reasoner / chat lines share the lower rate. ok=false for
// unknown ids.
func CostPer1k(model string) (float64, float64, bool) {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "v4-pro"):
		return 0.000435, 0.00087, true
	case strings.Contains(m, "deepseek"):
		return 0.00014, 0.00028, true
	}
	return 0, 0, false
}

// Capabilities reports what a DeepSeek model supports. The reasoner and v4
// lines expose chain-of-thought; chat (V3) does not. DeepSeek is text-only.
func Capabilities(model string) llm.ModelCapabilities {
	m := strings.ToLower(model)
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision:    false,
		SupportsThinking:  IsReasonerModel(model) || strings.Contains(m, "v4"),
	}
}
