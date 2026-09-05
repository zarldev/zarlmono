package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
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

// Complete constructs a fully lazy Gemini completion stream.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if cause := context.Cause(ctx); cause != nil {
			yield(llm.CompletionChunk{}, cause)
			return
		}
		p.streamCompletion(ctx, req, yield)
	}
}

// streamCompletion handles both streaming and single-shot requests through the
// SDK iterator so their cancellation, retry, and metadata semantics stay aligned.
func (p *Provider) streamCompletion(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	// Request conversion remains inside iterator invocation: Complete itself is
	// side-effect free and does not acquire an SDK stream.
	config := p.buildConfig(req)
	sys, contents := convertMessages(req.Messages)
	if sys != nil {
		config.SystemInstruction = sys
	}
	if len(contents) == 0 {
		yield(llm.CompletionChunk{}, errors.New("no valid messages"))
		return
	}

	const maxRetries = 4
	backoff := time.Second
	for attempt := 0; ; attempt++ {
		usage, usageReported, finishReason, calls, accepted, streamErr, downstream :=
			p.runStream(ctx, contents, config, yield)
		if !downstream {
			return
		}
		if streamErr == nil {
			if len(calls) > 0 {
				if !yield(llm.CompletionChunk{ToolCalls: calls}, nil) {
					return
				}
			}
			if finishReason != llm.FinishReasons.UNKNOWN || usageReported {
				if !yield(llm.CompletionChunk{
					FinishReason:  finishReason,
					Usage:         usage,
					UsageReported: usageReported,
				}, nil) {
					return
				}
			}
			return
		}

		if cause := context.Cause(ctx); cause != nil {
			yield(llm.CompletionChunk{}, cause)
			return
		}
		// Replaying a stream is safe only before downstream has accepted any
		// event. Cancellation, downstream false, exhausted budget, and every
		// non-rate-limit error are terminal.
		if !isRateLimit(streamErr) || attempt >= maxRetries || accepted {
			yield(llm.CompletionChunk{}, rateLimitError(streamErr, fmt.Errorf("gemini stream: %w", streamErr)))
			return
		}
		wait := backoffWithRetryAfter(streamErr, backoff)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			yield(llm.CompletionChunk{}, context.Cause(ctx))
			return
		case <-timer.C:
		}
		backoff *= 2
	}
}

// runStream consumes one SDK attempt. accepted means at least one event was
// synchronously accepted by downstream; false from downstream is returned
// separately and must terminate the invocation silently.
func (p *Provider) runStream(
	ctx context.Context,
	contents []*genai.Content,
	config *genai.GenerateContentConfig,
	yield func(llm.CompletionChunk, error) bool,
) (llm.Usage, bool, llm.FinishReason, []llm.ToolCall, bool, error, bool) {
	var usage llm.Usage
	var usageReported bool
	finishReason := llm.FinishReasons.UNKNOWN
	callsByID := make(map[string]llm.ToolCall)
	var order []string
	accepted := false

	for resp, err := range p.client.Models.GenerateContentStream(ctx, p.model, contents, config) {
		if err != nil {
			return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted, err, true
		}
		if resp.UsageMetadata != nil {
			usage = llm.Usage{
				PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
				CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
				TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
				CachedTokens:     int(resp.UsageMetadata.CachedContentTokenCount),
			}
			usageReported = true
		}
		if len(resp.Candidates) == 0 {
			continue
		}
		candidate := resp.Candidates[0]
		if candidate.FinishReason != "" {
			finishReason = normalizeFinishReason(candidate.FinishReason)
		}
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if !emitPart(part, callsByID, &order, yield) {
				return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted, nil, false
			}
			if part.Text != "" {
				accepted = true
			}
		}
	}
	return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted, nil, true
}

func emitPart(
	part *genai.Part,
	callsByID map[string]llm.ToolCall,
	order *[]string,
	yield func(llm.CompletionChunk, error) bool,
) bool {
	if part.Text != "" {
		chunk := llm.CompletionChunk{Content: part.Text}
		if part.Thought {
			chunk.Content = ""
			chunk.Thinking = part.Text
		}
		if !yield(chunk, nil) {
			return false
		}
	}
	if part.FunctionCall != nil {
		id := part.FunctionCall.ID
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, len(*order))
		}
		argBytes, _ := json.Marshal(part.FunctionCall.Args)
		if _, exists := callsByID[id]; !exists {
			*order = append(*order, id)
		}
		callsByID[id] = llm.ToolCall{
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

func orderedCalls(callsByID map[string]llm.ToolCall, order []string) []llm.ToolCall {
	if len(order) == 0 {
		return nil
	}
	calls := make([]llm.ToolCall, 0, len(order))
	for _, id := range order {
		calls = append(calls, callsByID[id])
	}
	return calls
}

func normalizeFinishReason(reason genai.FinishReason) llm.FinishReason {
	switch reason {
	case genai.FinishReasonStop:
		return llm.FinishReasons.STOP
	case genai.FinishReasonMaxTokens:
		return llm.FinishReasons.LENGTH
	case genai.FinishReasonSafety,
		genai.FinishReasonRecitation,
		genai.FinishReasonLanguage,
		genai.FinishReasonBlocklist,
		genai.FinishReasonProhibitedContent,
		genai.FinishReasonSPII,
		genai.FinishReasonImageSafety,
		genai.FinishReasonImageProhibitedContent,
		genai.FinishReasonImageRecitation:
		return llm.FinishReasons.CONTENTFILTER
	case genai.FinishReasonMalformedFunctionCall,
		genai.FinishReasonUnexpectedToolCall,
		genai.FinishReasonTooManyToolCalls:
		return llm.FinishReasons.TOOLCALLS
	default:
		return llm.FinishReasons.UNKNOWN
	}
}

// buildConfig produces the GenerateContentConfig for one request.
func (p *Provider) buildConfig(req llm.CompletionRequest) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{IncludeThoughts: true},
	}
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
// transports, or endpoint setups beyond WithBaseURL.
func WithClient(client *genai.Client) options.Option[Provider] {
	return func(p *Provider) {
		p.client = client
	}
}

// rateLimitError wraps a genai stream error as a *llm.RateLimitError when
// the error is a rate-limit (429), extracting the retry delay from the SDK's
// APIError details. Otherwise returns fallback unchanged.
func isRateLimit(err error) bool {
	apiErr, ok := errors.AsType[genai.APIError](err)
	return ok && apiErr.Code == 429
}

func backoffWithRetryAfter(err error, fallback time.Duration) time.Duration {
	apiErr, ok := errors.AsType[genai.APIError](err)
	if !ok {
		return fallback
	}
	for _, detail := range apiErr.Details {
		if delay, ok := detail["retryDelay"].(string); ok {
			if duration, parseErr := time.ParseDuration(delay); parseErr == nil && duration > 0 {
				return duration
			}
		}
	}
	return fallback
}

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
