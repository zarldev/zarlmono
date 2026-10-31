package deepseek

const (
	modelFlash              = "deepseek-flash"
	modelVisionExperimental = "deepseek-v4-flash-vision-exp"
)

// ContextWindowFor returns the published context window for known
// DeepSeek API model ids. Unknown ids return 0 so callers can fall
// back to probing or their generic unknown-model handling.
func ContextWindowFor(model string) int {
	switch model {
	case modelFlash, "deepseek-v4-pro", "deepseek-v4-flash", modelVisionExperimental, "deepseek-chat", "deepseek-reasoner":
		return 1_000_000
	}
	return 0
}

// IsReasonerModel reports whether model is the legacy DeepSeek R1 reasoner.
// R1's API 400s when a prior turn's reasoning_content is echoed back in the
// input messages, so reasoner history must be stripped. The current
// flash / pro models (deepseek-flash / deepseek-v4-pro) are the opposite —
// they round-trip reasoning_content across tool calls — and deepseek-chat
// (V3) emits no reasoning, so none of those are treated as a reasoner here.
func IsReasonerModel(model string) bool {
	return model == "deepseek-reasoner"
}
