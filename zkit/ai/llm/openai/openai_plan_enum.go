package openai

//go:generate go tool goenums -f openai_plan_enum.go

// endpointKind is the goenums source for EndpointKind — which OpenAI
// REST endpoint the request plan targets. The trailing comment on each
// constant is the stable wire/config name (what appears in the URL
// path); it is the single source of truth, so callers reference
// EndpointKinds.X rather than open-coding the string.
type endpointKind int

const (
	endpointChatCompletions endpointKind = iota // chat_completions
	endpointResponses                           // responses
)

// tokenLimitField is the goenums source for TokenLimitField — which
// JSON field carries the token cap for the target endpoint. The
// trailing comment is the stable wire/JSON name.
type tokenLimitField int

const (
	tokenLimitMaxTokens           tokenLimitField = iota // max_tokens
	tokenLimitMaxCompletionTokens                        // max_completion_tokens
	tokenLimitMaxOutputTokens                            // max_output_tokens
)

// reasoningEffort is the goenums source for ReasoningEffort — the
// effort level sent in Responses reasoning blocks. The trailing
// comment is the stable wire/JSON value.
type reasoningEffort int

const (
	reasoningEffortLow    reasoningEffort = iota // low
	reasoningEffortMedium                        // medium
	reasoningEffortHigh                          // high
	reasoningEffortXHigh                         // xhigh
	reasoningEffortMax                           // max
)
