package openaicodex

const (
	sseTypeMessage      = "message"
	sseTypeInputText    = "input_text"
	sseTypeFunctionCall = "function_call"
	sseTypeFunction     = "function"
	sseTypeReasoning    = "reasoning"

	codexContinuationProvider = "openai-codex"
	codexReasoningFormat      = "responses.reasoning.v1"
)

// Responses API endpoint path. Combined with the base URL
// (https://chatgpt.com/backend-api) this resolves to the canonical
// Codex endpoint.
const responsesPath = "/codex/responses"
