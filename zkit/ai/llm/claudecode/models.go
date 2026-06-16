package claudecode

// ListPresetModels returns Claude Code model aliases / ids accepted by the
// Claude Code CLI. Keep aliases first because they track Anthropic's current
// recommended model without requiring zarlcode releases for every model bump.
func ListPresetModels() []string {
	return []string{
		"sonnet",
		"opus",
		"claude-sonnet-4-6",
		"claude-opus-4-7",
		"claude-haiku-4-5",
	}
}

// ContextWindowFor returns the context window (in tokens) to assume for a
// Claude Code model alias or id, or 0 for the empty string ("unknown" —
// callers fall back to their own default).
func ContextWindowFor(model string) int {
	if model == "" {
		return 0
	}
	// Claude Code model aliases currently target Claude models with the
	// same context family as the Anthropic API. Return the conservative
	// default used by the anthropic backend.
	return 200_000
}
