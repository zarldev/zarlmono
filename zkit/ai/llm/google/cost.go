package google

import (
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// CostPer1k returns conservative standard USD per-1k-token estimates for
// verified Gemini Developer API text models. It excludes promotional discounts
// and uses the higher prompt-length tier where the API cannot express length.
// Audio, cache storage, batch, Live and Vertex billing require separate inputs.
// Unknown or retired model IDs return ok=false rather than family pricing.
// Source: https://ai.google.dev/gemini-api/docs/pricing.
func CostPer1k(model string) (float64, float64, bool) {
	switch strings.TrimPrefix(strings.ToLower(model), "models/") {
	case "gemini-4-argon":
		return 0.004, 0.020, true
	case "gemini-3.8-flash", "gemini-3.6-flash", "gemini-3.5-flash":
		return 0.00150, 0.00750, true
	case "gemini-3.5-flash-lite":
		return 0.00030, 0.00250, true
	case "gemini-3.1-flash-lite":
		return 0.00025, 0.00150, true
	case "gemini-3-flash-preview":
		return 0.00050, 0.00300, true
	case "gemini-3.1-pro-preview", "gemini-3.1-pro-preview-customtools":
		return 0.004, 0.018, true
	case "gemini-2.5-pro":
		return 0.00250, 0.015, true
	case "gemini-2.5-flash":
		return 0.00030, 0.00250, true
	case "gemini-2.5-flash-lite":
		return 0.00010, 0.00040, true
	default:
		return 0, 0, false
	}
}

// Capabilities reports what a Gemini model supports. Gemini is natively
// multimodal; the 2.x / 3.x / 4.x lines expose thinking.
func Capabilities(model string) llm.ModelCapabilities {
	m := strings.ToLower(model)
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision:    true,
		SupportsVideo:     true,
		SupportsThinking:  strings.Contains(m, "2.5") || strings.Contains(m, "thinking") || strings.HasPrefix(m, "gemini-3") || strings.HasPrefix(m, "gemini-4"),
	}
}
