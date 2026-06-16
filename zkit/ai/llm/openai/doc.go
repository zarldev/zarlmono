// Package openai adapts OpenAI-compatible chat completion APIs to the shared
// LLM provider interface.
//
// It wraps the official OpenAI Go SDK for normal OpenAI usage and supports
// provider options for model, timeout, and base URL so compatible endpoints can
// share the same conversion and streaming code.
package openai
