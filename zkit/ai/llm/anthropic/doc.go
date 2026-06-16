// Package anthropic adapts Anthropic Claude models to the shared LLM provider
// interface.
//
// It wraps the official Anthropic SDK, applies provider options, keeps optional
// per-conversation history, and converts between the repository's provider-neutral
// request/response types and Anthropic message formats.
package anthropic
