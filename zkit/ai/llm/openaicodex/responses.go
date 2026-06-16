package openaicodex

const (
	sseTypeMessage      = "message"
	sseTypeInputText    = "input_text"
	sseTypeFunctionCall = "function_call"
	sseTypeFunction     = "function"
)

// Responses API endpoint path. Combined with the base URL
// (https://chatgpt.com/backend-api) this resolves to the canonical
// Codex endpoint.
const responsesPath = "/codex/responses"
