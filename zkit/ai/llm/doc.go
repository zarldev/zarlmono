// Package llm defines zkit's provider-neutral language-model contract.
//
// The core abstraction is Provider: a deliberately narrow interface with one
// streaming completion method and one name method. Provider implementations live
// in subpackages such as openai, anthropic, google, ollama, llamacpp, deepseek,
// claudecode, and openaicodex. Consumers should depend on this package's
// request, response, tool-call, response-format, and streaming types rather than
// on provider SDK DTOs.
//
// Streaming providers must own and close the returned channel and emit exactly
// one terminal CompletionChunk with Done set. On success the terminal chunk
// carries final usage/finish metadata when available. On failure the terminal
// chunk carries Error and Done. This lets runners and shells treat stream
// completion consistently across providers.
//
// Richer capabilities, such as model discovery or OAuth-backed construction,
// should remain separate opt-in interfaces or backend helpers rather than
// widening Provider.
package llm
