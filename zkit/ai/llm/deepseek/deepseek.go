// Package deepseek provides an LLM provider for DeepSeek's
// OpenAI-compatible chat completions API.
package deepseek

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

// DefaultBaseURL is DeepSeek's OpenAI-compatible API endpoint.
const DefaultBaseURL = "https://api.deepseek.com"

// DefaultModel is the hosted chat model used when callers do not
// choose a model explicitly.
const DefaultModel = "deepseek-chat"

// roleUser is the message role the schema directive is folded into.
const roleUser = "user"

// Provider is a thin DeepSeek-named facade over the OpenAI-compatible
// transport implementation.
type Provider struct {
	inner   llm.Provider
	baseURL string
	model   string
	timeout time.Duration
}

// NewProvider creates a DeepSeek-targeted provider. Empty apiKey errors;
// empty baseURL defaults to DefaultBaseURL; empty model defaults to
// DefaultModel.
func NewProvider(apiKey string, opts ...options.Option[Provider]) (llm.Provider, error) {
	p := &Provider{
		baseURL: DefaultBaseURL,
		model:   DefaultModel,
	}
	for _, opt := range opts {
		opt(p)
	}

	// V4 requires reasoning_content echoed back across tool calls;
	// R1 (deepseek-reasoner) rejects it. See IsReasonerModel.
	reasoning := llm.ReasoningHistories.FIELD
	if IsReasonerModel(p.model) {
		reasoning = llm.ReasoningHistories.STRIP
	}
	optsInner := []options.Option[openai.Provider]{
		openai.WithBaseURL(p.baseURL),
		openai.WithModel(p.model),
		openai.WithReasoningHistory(reasoning),
	}
	// V4's Field-mode trim: keep reasoning_content only on assistant
	// turns inside a tool-call window, drop it elsewhere to save input
	// tokens. R1 is on Strip globally so the mask is moot.
	if reasoning == llm.ReasoningHistories.FIELD {
		optsInner = append(optsInner, openai.WithReasoningKeepMask(keepReasoningMask))
	}
	if p.timeout > 0 {
		optsInner = append(optsInner, openai.WithTimeout(p.timeout))
	}

	inner, err := openai.NewProvider(apiKey, optsInner...)
	if err != nil {
		return nil, err
	}
	p.inner = inner
	return p, nil
}

// Name returns the provider name.
func (p *Provider) Name() string { return llm.LLMProviders.DEEPSEEK.String() }

// Complete delegates to the OpenAI-compatible transport's native iter.Seq2
// stream after rewriting any response_format DeepSeek's hosted API can't
// accept (see adaptResponseFormat).
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return p.inner.Complete(ctx, adaptResponseFormat(req))
}

// adaptResponseFormat rewrites a request's response_format into the shape
// DeepSeek's /chat/completions actually supports. DeepSeek accepts only
// `text` and `json_object`: a `json_schema` type 400s outright
// ("response_format.type json_schema is unavailable now"), and even
// `json_object` 400s unless the prompt literally contains the word
// "json".
//
// Callers that want json_schema (the spawn planner, the decompose judge)
// rely on a closed enum, but they ALSO defensively validate the parsed
// result and treat any mismatch as a soft fallback. So we can safely
// downgrade to json_object and fold the schema into a trailing prompt
// directive — that hands the model the exact shape AND satisfies the
// keyword rule in one move. The constraint is then enforced downstream
// rather than by the (unavailable) server-side grammar.
//
// req is taken by value; we only ever replace its Messages slice with a
// fresh copy, so the caller's slice is never mutated.
func adaptResponseFormat(req llm.CompletionRequest) llm.CompletionRequest {
	switch req.ResponseFormat.Type {
	case llm.ResponseFormatJSONSchema:
		req.Messages = appendJSONDirective(req.Messages, schemaDirective(req.ResponseFormat.Schema.Map()))
		req.ResponseFormat = llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}
	case llm.ResponseFormatJSONObject:
		if !messagesMentionJSON(req.Messages) {
			req.Messages = appendJSONDirective(req.Messages, schemaDirective(nil))
		}
	}
	return req
}

// schemaDirective renders the prompt instruction that both satisfies
// DeepSeek's "prompt must contain 'json'" rule and, when a schema is
// supplied, pins the expected shape. A non-marshalable schema falls back
// to the bare directive rather than failing the request.
func schemaDirective(schema map[string]any) string {
	const base = "Respond with ONLY a single JSON object — no prose, no markdown fences."
	if len(schema) == 0 {
		return base
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return base
	}
	return base + " It MUST conform to this JSON schema: " + string(raw)
}

// appendJSONDirective returns a copy of msgs with directive folded into
// the last plain-text user message (keeping the instruction most salient
// at the end of the turn). If there's no such message, it appends a new
// user message carrying the directive.
func appendJSONDirective(msgs []llm.Message, directive string) []llm.Message {
	out := append([]llm.Message(nil), msgs...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == roleUser && len(out[i].Parts) == 0 {
			if out[i].Content != "" {
				out[i].Content += "\n\n"
			}
			out[i].Content += directive
			return out
		}
	}
	return append(out, llm.Message{Role: roleUser, Content: directive})
}

// messagesMentionJSON reports whether any message content already
// contains the word "json" (case-insensitive), satisfying DeepSeek's
// json_object prerequisite without an injected directive.
func messagesMentionJSON(msgs []llm.Message) bool {
	for _, m := range msgs {
		if strings.Contains(strings.ToLower(m.Content), "json") {
			return true
		}
	}
	return false
}

// WithBaseURL sets the DeepSeek API base URL. Empty string leaves the
// default (https://api.deepseek.com) in place.
func WithBaseURL(baseURL string) options.Option[Provider] {
	return func(p *Provider) {
		if baseURL != "" {
			p.baseURL = baseURL
		}
	}
}

// WithModel sets the default model. Empty string leaves the default
// (deepseek-chat) in place.
func WithModel(model string) options.Option[Provider] {
	return func(p *Provider) {
		if model != "" {
			p.model = model
		}
	}
}

// WithTimeout sets the HTTP request timeout. Zero leaves the openai
// package default in place.
func WithTimeout(d time.Duration) options.Option[Provider] {
	return func(p *Provider) {
		p.timeout = d
	}
}
