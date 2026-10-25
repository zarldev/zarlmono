package anthropic

import (
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ContextWindowFor returns the published context window for a
// Claude model id. Centralises per-model knowledge that used to
// live in backends.anthropicContextWindow as a suffix-matching
// heuristic — moving it here puts model metadata next to the
// provider implementation and replaces the heuristic with an
// explicit table.
//
// Claude Opus 4.6/4.7 and Sonnet 4.6 have a native 1M context window.
// Recognized legacy picker suffixes are aliases, not arbitrary capacity hints.
//
// Unknown ids return 0 ("unknown") so the caller's probe /
// fallback chain stays in charge. The previous behaviour of
// silently defaulting to 200k masked typos and made it impossible
// to detect a model entirely missing from the table.
func ContextWindowFor(model string) int {
	if model == "" {
		return 0
	}
	switch wireModelID(model) {
	case modelClaudeOpus47, modelClaudeOpus46, modelClaudeSonnet46:
		return 1_000_000
	case "claude-opus-4-5", "claude-sonnet-4-5", "claude-haiku-4-5":
		return 200_000

	// --- Claude 3.5 / 3.7 (still widely deployed) ---
	case "claude-3-7-sonnet-latest", "claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-latest", "claude-3-5-sonnet-20241022",
		"claude-3-5-sonnet-20240620",
		"claude-3-5-haiku-latest", "claude-3-5-haiku-20241022":
		return 200_000

	// --- Claude 3 (legacy) ---
	case "claude-3-opus-20240229", "claude-3-opus-latest",
		"claude-3-sonnet-20240229",
		"claude-3-haiku-20240307":
		return 200_000
	}
	return 0
}

// CostPer1k returns the published USD per-1k-token (input, output) rate for
// a Claude model, matched by family. ok=false for ids that don't match a
// known family, so the caller shows "unknown rate" rather than a wrong
// number. Rates are approximate and drift as Anthropic re-prices.
func CostPer1k(model string) (float64, float64, bool) {
	m := wireModelID(strings.ToLower(model))
	switch m {
	case modelClaudeOpus47, modelClaudeOpus46, "claude-opus-4-5":
		return 0.005, 0.025, true
	case "claude-3-opus-20240229", "claude-3-opus-latest":
		return 0.015, 0.075, true
	case modelClaudeSonnet46, "claude-sonnet-4-5", "claude-3-7-sonnet-latest", "claude-3-7-sonnet-20250219", "claude-3-5-sonnet-latest", "claude-3-5-sonnet-20241022":
		return 0.003, 0.015, true
	case "claude-haiku-4-5":
		return 0.001, 0.005, true
	case "claude-3-5-haiku-latest", "claude-3-5-haiku-20241022":
		return 0.0008, 0.004, true
	case "claude-3-haiku-20240307":
		return 0.00025, 0.00125, true
	}
	return 0, 0, false
}

// Capabilities reports what a Claude model supports. Every current Claude
// model streams, calls tools, takes a system prompt, and accepts images;
// extended thinking is a 4.x / 3.7 feature.
func Capabilities(model string) llm.ModelCapabilities {
	m := strings.ToLower(model)
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision:    true,
		SupportsThinking: adaptiveThinkingModel(m) || strings.Contains(m, "-4-") ||
			strings.Contains(m, "opus-4") || strings.Contains(m, "sonnet-4") ||
			strings.Contains(m, "haiku-4") || strings.Contains(m, "3-7-sonnet"),
	}
}
