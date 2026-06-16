package llm

//go:generate go tool goenums -f reasoninghistory_enum.go

// reasoningHistory is the goenums source for ReasoningHistory — how a provider
// echoes prior-turn assistant reasoning back into request history. The trailing
// comment on each constant is the stable wire/DB name (what is persisted in the
// llm_providers row); it is the single source of truth, so callers reference
// llm.ReasoningHistories.X rather than open-coding the string.
type reasoningHistory int

const (
	// inline re-wraps reasoning as <think>…</think> inside content.
	inline reasoningHistory = iota // inline
	// field sends reasoning in the dedicated reasoning_content field; thinking
	// models (Moonshot/Kimi, DeepSeek-V4) require this across tool calls.
	field // field
	// strip drops prior-turn reasoning entirely (deepseek-reasoner/R1).
	strip // strip
)
