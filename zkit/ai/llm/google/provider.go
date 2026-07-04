package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"math"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
	"google.golang.org/genai"
)

// defaultModel is the model both constructors fall back to when
// WithModel is not supplied.
const defaultModel = "gemini-2.0-flash"

// Provider implements the Google Gemini LLM provider using the official Go SDK.
type Provider struct {
	apiKey  string
	client  *genai.Client
	model   string
	baseURL string // optional override for HTTPOptions.BaseURL (tests / private proxy)
}

// NewProvider creates a Generative Language API (AI Studio) provider
// authenticated by apiKey. All Google API keys (AIza... and AQ...)
// target that surface — the key-prefix heuristic that briefly routed
// AQ. keys to Vertex was wrong; Google issues AQ. keys against AI
// Studio as well.
//
// Vertex AI is a separate surface with no API keys: use
// [NewVertexProvider] (ADC-authenticated), or inject any
// fully-configured client via [WithClient] — apiKey may then be empty.
func NewProvider(apiKey string, opts ...options.Option[Provider]) (*Provider, error) {
	provider := &Provider{
		apiKey: apiKey,
		model:  defaultModel,
	}

	// Apply options FIRST so the genai client picks up any baseURL
	// override. The earlier shape built the client first then
	// applied options, so WithBaseURL had no effect — the client
	// was already pointed at generativelanguage.googleapis.com.
	for _, opt := range opts {
		opt(provider)
	}

	// An injected client (WithClient) wins outright — it carries its
	// own backend, credentials, and endpoint, so the API key is not
	// required and no client is built here.
	if provider.client != nil {
		return provider, nil
	}
	if apiKey == "" {
		return nil, llm.ErrInvalidAPIKey
	}

	cfg := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	}
	if provider.baseURL != "" {
		cfg.HTTPOptions.BaseURL = provider.baseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new google client: %w", err)
	}
	provider.client = client

	return provider, nil
}

// NewVertexProvider creates a Vertex AI provider for the given project
// and location, authenticating via Application Default Credentials —
// the genai SDK's default when no explicit credentials are supplied
// (`gcloud auth application-default login` locally, the attached
// service account on GCP). API keys play no part; Vertex doesn't use
// them. Empty project/location fall back to the SDK's
// GOOGLE_CLOUD_PROJECT / GOOGLE_CLOUD_LOCATION environment lookup.
//
// ctx bounds credential discovery and client construction. For custom
// credentials, transports, or endpoints beyond what this covers, build
// the genai client yourself and inject it with [WithClient].
func NewVertexProvider(ctx context.Context, project, location string, opts ...options.Option[Provider]) (*Provider, error) {
	provider := &Provider{
		model: defaultModel,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.client != nil {
		return provider, nil
	}

	cfg := &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  project,
		Location: location,
	}
	if provider.baseURL != "" {
		cfg.HTTPOptions.BaseURL = provider.baseURL
	}
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new vertex client: %w", err)
	}
	provider.client = client

	return provider, nil
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return llm.LLMProviders.GOOGLE.String()
}

// Complete generates a completion as a native iter.Seq2 — the stream's
// chunks are yielded directly (errors as the second value), no channel.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if req.Stream {
			p.streamCompletion(ctx, req, yield)
		} else {
			p.nonStreamCompletion(ctx, req, yield)
		}
	}, nil
}

// streamCompletion handles streaming responses using the SDK's
// Models.GenerateContentStream — gives us access to the typed Parts
// (Text + FunctionCall) which the chat session abstraction hides.
//
// Wraps the call in retry-on-429 with exponential backoff so transient
// rate-limit hits don't kill an agent turn — Gemini's free-tier RPM is
// low enough that a single turn doing many tool iterations can trip
// it mid-flight.
func (p *Provider) streamCompletion(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	slog.DebugContext(ctx, "google streaming completion",
		"model", p.model,
		"messages", len(req.Messages),
		"tools", len(req.Tools))

	config := p.buildConfig(req)
	sys, contents := convertMessages(req.Messages)
	if sys != nil {
		config.SystemInstruction = sys
	}
	if len(contents) == 0 {
		// Terminal error path. Yield Done:true so downstream readers
		// see one canonical terminal chunk; returning then signals EOF
		// to the consumer's range loop.
		yield(llm.CompletionChunk{Done: true}, errors.New("no valid messages"))
		return
	}

	const maxRetries = 4
	backoff := time.Second

	for attempt := 0; ; attempt++ {
		// Each attempt re-streams the whole response from scratch; runStream
		// returns a fresh streamAttempt, so a failed prior attempt leaves no
		// stale tool-call fragments behind.
		st, streamErr, ok := p.runStream(ctx, contents, config, yield)
		if !ok {
			// Consumer broke out of the range — stop without yielding again.
			return
		}
		if streamErr == nil {
			// Emit accumulated tool calls in arrival order, then close.
			if len(st.order) > 0 {
				calls := make([]llm.ToolCall, 0, len(st.order))
				for _, id := range st.order {
					calls = append(calls, st.calls[id])
				}
				if !yield(llm.CompletionChunk{ToolCalls: calls}, nil) {
					return
				}
			}
			yield(llm.CompletionChunk{Done: true, Usage: st.usage}, nil)
			return
		}
		// Retry only a rate-limit, only within budget, and only if nothing
		// visible was emitted this attempt — a re-stream would replay it, so
		// once st.emitted is set the 429 is terminal, not retryable.
		if !isRateLimit(streamErr) || attempt >= maxRetries || st.emitted {
			// User-cancel comes back as ctx.Canceled / DeadlineExceeded.
			// That's an expected outcome, not an error — log at Debug
			// so an active TUI's stdout-tee'd slog handler doesn't paint
			// "ERROR google stream" over the alt-screen on every cancel.
			if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
				slog.DebugContext(ctx, "google stream cancelled", "error", streamErr, "attempt", attempt)
			} else {
				slog.ErrorContext(ctx, "google stream", "error", streamErr, "attempt", attempt)
			}
			yield(llm.CompletionChunk{Done: true}, rateLimitError(streamErr, fmt.Errorf("gemini stream: %w", streamErr)))
			return
		}
		wait := backoffWithRetryAfter(streamErr, backoff)
		slog.WarnContext(ctx, "google rate limit, backing off", "wait", wait, "attempt", attempt)
		select {
		case <-ctx.Done():
			yield(llm.CompletionChunk{Done: true}, ctx.Err())
			return
		case <-time.After(wait):
		}
		backoff *= 2
	}
}

// streamAttempt is what one runStream pass accumulated: the latest usage
// snapshot, tool calls in arrival order, and whether any visible chunk reached
// the consumer (which makes a re-stream unsafe). Returning this beats threading
// pointer out-params back through runStream.
type streamAttempt struct {
	usage   *llm.Usage
	calls   map[string]llm.ToolCall
	order   []string
	emitted bool
}

// runStream consumes one full stream attempt and returns what it accumulated.
// The error return is nil on clean completion, non-nil otherwise — including
// RPM-cap 429s the caller may retry. The bool return is false when the consumer
// broke out of the range (yield returned false): the caller must stop without
// yielding or retrying.
func (p *Provider) runStream(
	ctx context.Context,
	contents []*genai.Content,
	config *genai.GenerateContentConfig,
	yield func(llm.CompletionChunk, error) bool,
) (streamAttempt, error, bool) {
	// Gemini emits thought parts (part.Thought == true) interleaved
	// with regular text. Route those bytes through the runner's
	// out-of-band Thinking channel — Content stays the visible answer.
	st := streamAttempt{calls: map[string]llm.ToolCall{}}
	for resp, err := range p.client.Models.GenerateContentStream(ctx, p.model, contents, config) {
		if err != nil {
			return st, err, true
		}
		if resp.UsageMetadata != nil {
			st.usage = &llm.Usage{
				PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
				CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
				TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
				CachedTokens:     int(resp.UsageMetadata.CachedContentTokenCount),
			}
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			continue
		}
		for _, part := range resp.Candidates[0].Content.Parts {
			if !emitPart(part, &st, yield) {
				return st, nil, false
			}
		}
	}
	return st, nil, true
}

// emitPart routes a single genai.Part to the yield func: thought parts go to
// the Thinking channel, non-thought text to Content, and function calls
// accumulate into pendingCalls for runStream to flush. Returns false when
// yield reports the consumer broke out of the range, so callers stop early.
func emitPart(part *genai.Part, st *streamAttempt, yield func(llm.CompletionChunk, error) bool) bool {
	if part.Text != "" {
		// Mark before yielding: any visible chunk reaching the consumer makes
		// a subsequent re-stream unsafe (it would duplicate this content).
		st.emitted = true
		if part.Thought {
			if !yield(llm.CompletionChunk{Thinking: part.Text}, nil) {
				return false
			}
		} else {
			if !yield(llm.CompletionChunk{Content: part.Text}, nil) {
				return false
			}
		}
	}
	if part.FunctionCall != nil {
		id := part.FunctionCall.ID
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, len(st.order))
		}
		argBytes, _ := json.Marshal(part.FunctionCall.Args)
		if _, exists := st.calls[id]; !exists {
			st.order = append(st.order, id)
		}
		st.calls[id] = llm.ToolCall{
			ID:   id,
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      part.FunctionCall.Name,
				Arguments: string(argBytes),
			},
		}
	}
	return true
}

// isRateLimit returns true when err carries a 429 from the genai SDK. It
// keys off the typed APIError's HTTP status code — the authoritative signal —
// rather than scanning the message text for "429"/"rate limit", which is
// fragile (ordinary messages can contain those substrings).
func isRateLimit(err error) bool {
	if err == nil {
		return false
	}
	apiErr, ok := errors.AsType[genai.APIError](err)
	return ok && apiErr.Code == 429
}

// backoffWithRetryAfter returns the sleep duration to wait before the
// next retry. Honours a Retry-After hint inside the genai APIError when
// present; falls back to the caller-supplied exponential value.
func backoffWithRetryAfter(err error, fallback time.Duration) time.Duration {
	apiErr, ok := errors.AsType[genai.APIError](err)
	if !ok {
		return fallback
	}
	for _, d := range apiErr.Details {
		if t, ok := d["retryDelay"].(string); ok {
			if dur, perr := time.ParseDuration(t); perr == nil && dur > 0 {
				return dur
			}
		}
	}
	return fallback
}

// nonStreamCompletion is the same flow, single-shot. Delegates to the
// streaming path under the hood — fewer surface-area divergence
// surprises this way.
func (p *Provider) nonStreamCompletion(
	ctx context.Context,
	req llm.CompletionRequest,
	yield func(llm.CompletionChunk, error) bool,
) {
	p.streamCompletion(ctx, req, yield)
}

// buildConfig produces the GenerateContentConfig for one request.
// Tools and system prompt are attached in streamCompletion since they
// derive from per-call data.
//
// IncludeThoughts defaults to true on every request: Gemini's thinking
// models (2.5-pro, 2.5-flash, 2.0-flash-thinking, …) silently emit
// reasoning when asked. Forwarding the thoughts lets the cockpit's
// live-reasoning pane surface them; non-thinking models silently
// ignore the flag. Cost is unchanged either way — the bill is the
// thoughtsTokenCount, and skipping IncludeThoughts only suppresses
// the transmission, not the work.
func (p *Provider) buildConfig(req llm.CompletionRequest) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
		},
	}
	// Only set temperature when explicitly requested. The zero value would
	// otherwise pin Gemini to a deterministic 0.0 instead of letting the
	// server apply the model's own default — matching openai/anthropic,
	// which both gate on Temperature > 0.
	if req.Temperature > 0 {
		cfg.Temperature = new(req.Temperature)
	}
	if req.MaxTokens > 0 {
		if req.MaxTokens > math.MaxInt32 {
			cfg.MaxOutputTokens = math.MaxInt32
		} else {
			cfg.MaxOutputTokens = int32(req.MaxTokens)
		}
	}
	if tools := convertTools(req.Tools); len(tools) > 0 {
		cfg.Tools = tools
	}
	applyResponseFormat(cfg, req.ResponseFormat)
	return cfg
}

// mimeJSON is the response MIME type Gemini's structured-output modes set.
const mimeJSON = "application/json"

// applyResponseFormat maps the provider-neutral ResponseFormat onto Gemini's
// native structured-output config. JSON-object mode sets the response MIME
// type; JSON-schema mode additionally constrains output to the supplied
// schema, passed verbatim through ResponseJsonSchema (the SDK's raw-schema
// escape hatch — it accepts a plain JSON Schema map, mirroring what OpenAI
// and llama.cpp grammar-constrain). The zero value (text) is a no-op.
func applyResponseFormat(cfg *genai.GenerateContentConfig, rf llm.ResponseFormat) {
	switch rf.Type {
	case llm.ResponseFormatJSONObject:
		cfg.ResponseMIMEType = mimeJSON
	case llm.ResponseFormatJSONSchema:
		cfg.ResponseMIMEType = mimeJSON
		if !rf.Schema.IsZero() {
			cfg.ResponseJsonSchema = rf.Schema.Map()
		}
	}
}

// Option functions for configuring the Google provider

// WithBaseURL overrides the genai SDK's default endpoint. Empty
// string leaves the SDK default (generativelanguage.googleapis.com)
// in place. Used by conformance tests and any consumer pointing at
// a private proxy / mirror.
func WithBaseURL(baseURL string) options.Option[Provider] {
	return func(p *Provider) {
		p.baseURL = baseURL
	}
}

// WithModel sets the default model for the provider.
func WithModel(model string) options.Option[Provider] {
	return func(p *Provider) {
		if model != "" {
			p.model = model
		}
	}
}

// WithClient injects a fully-configured genai client; the constructors
// then skip their own client construction entirely, so the client's
// backend, credentials, and endpoint win — and NewProvider's apiKey may
// be empty. This is the escape hatch for configurations the
// constructors don't model: explicit Vertex credentials, custom
// transports, or endpoint setups beyond WithBaseURL. A nil client is
// ignored.
func WithClient(client *genai.Client) options.Option[Provider] {
	return func(p *Provider) {
		if client != nil {
			p.client = client
		}
	}
}

// rateLimitError wraps a genai stream error as a *llm.RateLimitError when
// the error is a rate-limit (429), extracting the retry delay from the SDK's
// APIError details. Otherwise returns fallback unchanged.
func rateLimitError(err error, fallback error) error {
	if !isRateLimit(err) {
		return fallback
	}
	msg := "gemini rate limit"
	if apiErr, ok := errors.AsType[genai.APIError](err); ok && apiErr.Message != "" {
		msg = apiErr.Message
	}
	return &llm.RateLimitError{
		Message:    msg,
		RetryAfter: backoffWithRetryAfter(err, 0),
	}
}
