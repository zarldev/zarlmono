// Package anthropic adapts Anthropic Claude models to the shared LLM provider
// interface.
//
// It wraps the official Anthropic SDK without retaining conversation state.
// Each lazy completion consumes the supplied provider-neutral request, applies
// model-aware request policy, and preserves captured native continuation blocks.
// Streaming and nonstreaming calls share request shaping and usage semantics.
package anthropic
