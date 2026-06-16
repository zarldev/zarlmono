// Package google adapts Google Gemini models to the shared LLM provider
// interface.
//
// It wraps the google.golang.org/genai client and converts provider-neutral chat,
// model, and streaming requests into Gemini API calls. API-key based clients use
// the Gemini API backend; callers that need Vertex-specific configuration should
// provide an appropriately configured client through provider options.
package google
