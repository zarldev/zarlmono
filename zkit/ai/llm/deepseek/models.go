package deepseek

// ContextWindowFor returns the published context window for known
// DeepSeek API model ids. Unknown ids return 0 so callers can fall
// back to probing or their generic unknown-model handling.
func ContextWindowFor(model string) int {
	switch model {
	case "deepseek-v4-flash", "deepseek-v4-pro", "deepseek-chat", "deepseek-reasoner":
		return 1_000_000
	}
	return 0
}

// IsReasonerModel reports whether model is a DeepSeek R1 reasoner. R1's
// API 400s when a prior turn's reasoning_content is echoed back in the
// input messages, so reasoner history must be stripped. V4 models
// (v4-flash / v4-pro) are the opposite — they *require* reasoning_content
// round-tripped across tool calls — and deepseek-chat (V3) emits no
// reasoning at all, so neither is treated as a reasoner here.
func IsReasonerModel(model string) bool {
	return model == "deepseek-reasoner"
}
