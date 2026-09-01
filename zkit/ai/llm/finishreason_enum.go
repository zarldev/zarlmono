package llm

//go:generate go tool goenums -f finishreason_enum.go

// finishReason is the goenums source for FinishReason, the provider-neutral
// semantic reason a model stopped producing output. Provider adapters normalize
// their wire values to this vocabulary. Unknown or unsupported values map to
// FinishReasons.UNKNOWN; transport and protocol failures remain stream errors.
type finishReason int

const (
	// unknown means no finish reason was reported or the provider value is not
	// recognized. It is the semantic zero value.
	unknown finishReason = iota // unknown
	// stop means the model completed normally, including provider stop sequences.
	stop // stop
	// length means a model or provider token/output limit ended generation.
	length // length
	// toolCalls means generation paused to request one or more tool calls.
	toolCalls // tool_calls
	// contentFilter means a provider safety or content policy ended generation.
	contentFilter // content_filter
)
