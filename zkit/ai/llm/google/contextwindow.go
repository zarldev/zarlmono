package google

import "strings"

// ContextWindowFor returns the documented context or input-token budget for known
// Gemini text models, or zero for an unknown model. Gemini 4 Argon's limit is a
// unified input-plus-output budget; other listed limits are input-token budgets.
// This is not a claim that a model is available on every Gemini/Vertex account
// or region: https://ai.google.dev/gemini-api/docs/models.
func ContextWindowFor(model string) int {
	switch strings.TrimPrefix(strings.ToLower(model), "models/") {
	case "gemini-1.5-pro", "gemini-1.5-pro-001", "gemini-1.5-pro-002":
		return 2_097_152
	case "gemini-1.5-flash", "gemini-1.5-flash-001", "gemini-1.5-flash-002",
		"gemini-2.0-flash", "gemini-2.0-flash-001", "gemini-2.0-flash-lite", "gemini-2.0-flash-lite-001",
		"gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.5-pro",
		"gemini-3.5-flash", "gemini-3.8-flash", "gemini-4-argon":
		return 1_048_576
	default:
		return 0
	}
}
