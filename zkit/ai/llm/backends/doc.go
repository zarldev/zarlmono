// Package backends maps user-facing backend names to concrete LLM provider
// implementations.
//
// It centralizes provider defaults, model metadata, OAuth gating, context-window
// lookup, and provider construction. Individual provider packages remain the
// source of transport-specific streaming behavior.
package backends
