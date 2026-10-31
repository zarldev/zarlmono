package deepseek

import (
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// CostPer1k returns a conservative standard USD per-1k-token estimate for
// verified model IDs. Input uses cache-miss pricing and both rates use peak
// tariffs, not an unqualified off-peak discount. Unknown/retired aliases return
// ok=false. See https://api-docs.deepseek.com/quick_start/pricing/.
func CostPer1k(model string) (float64, float64, bool) {
	switch strings.ToLower(model) {
	case "deepseek-v4-pro":
		return 0.00132, 0.00396, true
	case modelFlash, "deepseek-v4-flash", modelVisionExperimental:
		return 0.00030, 0.00120, true
	default:
		return 0, 0, false
	}
}

// Capabilities reports what a DeepSeek model supports. The flash line
// (deepseek-flash) and legacy reasoner / v4 lines expose chain-of-thought;
// chat (V3) does not. deepseek-flash is the vision-capable tier.
func Capabilities(model string) llm.ModelCapabilities {
	m := strings.ToLower(model)
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision:    m == modelFlash || m == modelVisionExperimental,
		SupportsThinking:  IsReasonerModel(model) || strings.Contains(m, "v4") || m == modelFlash,
	}
}
