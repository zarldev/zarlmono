package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"math"
	"net/http"
	"slices"
	"time"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

const defaultModel = "gemini-3.8-flash"

const (
	continuationProvider     = "google"
	legacyContinuationFormat = "generate_content.part.v1"
	// v2 retains every SDK part (including unsigned display text) in stream
	// order. Unlike v1's signed-call subset, it owns the full replay projection.
	continuationFormat = "generate_content.part.v2"
)

// Provider implements the Google Gemini LLM provider using the official Go SDK.
type Provider struct {
	interactionsAPI bool
	apiKey          string
	client          *genai.Client
	model           string
	baseURL         string       // optional override for HTTPOptions.BaseURL (tests / private proxy)
	httpClient      *http.Client // optional, borrowed SDK transport policy
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
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: provider.httpClient,
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
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
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
		Backend:    genai.BackendVertexAI,
		Project:    project,
		Location:   location,
		HTTPClient: provider.httpClient,
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
		if p.interactionsAPI {
			p.streamInteractions(ctx, req, yield)
			return
		}
		p.streamCompletion(ctx, req, yield)
	}
}

// streamCompletion selects the native SDK operation while sharing response
// conversion, cancellation, retry, and final metadata semantics.
func (p *Provider) streamCompletion(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	// Request conversion remains inside iterator invocation: Complete itself is
	// side-effect free and does not acquire an SDK stream.
	expandedMessages := llm.ExpandToolResultParts(req.Messages)
	if err := validateMessages(expandedMessages); err != nil {
		yield(llm.CompletionChunk{}, err)
		return
	}
	config := p.buildConfig(req)
	if err := p.applyThinkingOptions(config, req); err != nil {
		yield(llm.CompletionChunk{}, err)
		return
	}
	sys, contents, err := convertMessages(ctx, expandedMessages)
	if err != nil {
		yield(llm.CompletionChunk{}, err)
		return
	}
	if cause := context.Cause(ctx); cause != nil {
		yield(llm.CompletionChunk{}, cause)
		return
	}
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
		// Each SDK construction gets independent restoration and ExtraBody
		// snapshots: caller hooks may mutate them before a retry is needed.
		config.HTTPOptions, err = p.requestHTTPOptions(ctx, contents)
		if err != nil {
			yield(llm.CompletionChunk{}, err)
			return
		}
		usage, usageReported, finishReason, calls, accepted, streamErr, downstream :=
			p.runStream(ctx, contents, config, req.Stream, yield)
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

// ErrIncompatibleContentsOverride identifies a static client ExtraBody.contents
// override that cannot preserve the converted model history's turn mapping or parts.
var ErrIncompatibleContentsOverride = errors.New("google: incompatible client ExtraBody.contents override")

// requestHTTPOptions composes request-local restoration with the client's hook.
// The pinned SDK merges ExtraBody before the request hook and replaces, rather
// than composes, client hooks. Its JSON copy also drops the client's hook, so even
// requests without model history must explicitly forward a configured hook.
func (p *Provider) requestHTTPOptions(ctx context.Context, contents []*genai.Content) (*genai.HTTPOptions, error) {
	clientOptions := p.client.ClientConfig().HTTPOptions
	restore := false
	for _, content := range contents {
		if content.Role == string(genai.RoleModel) {
			restore = true
			break
		}
	}
	if !restore {
		return &genai.HTTPOptions{ExtrasRequestProvider: clientOptions.ExtrasRequestProvider}, nil
	}

	// Both SDK backend converters expose parts as []map[string]any. Prepare
	// that shape with exact numbers before entering the no-error-return hook,
	// rather than leaking SDK structs or the converter's rounded float64 data.
	modelParts := make(map[int][]map[string]any)
	for index, content := range contents {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		if content.Role != string(genai.RoleModel) {
			continue
		}
		data, err := json.Marshal(content.Parts)
		if err != nil {
			return nil, errors.New("google: encode model parts")
		}
		var parts []map[string]any
		if err := decodeReplayJSON(data, &parts); err != nil {
			return nil, errors.New("google: decode model parts")
		}
		modelParts[index] = parts
	}

	// The request owns this copy, including nested maps/slices. Passing it as a
	// request override avoids the SDK's lossy float64 copy of client ExtraBody
	// and prevents restoration or the caller's hook from mutating client state.
	var extraBody map[string]any
	if clientOptions.ExtraBody != nil {
		data, err := json.Marshal(clientOptions.ExtraBody)
		if err != nil {
			return nil, errors.New("google: encode client ExtraBody")
		}
		if err := decodeReplayJSON(data, &extraBody); err != nil {
			return nil, errors.New("google: decode client ExtraBody")
		}
		if override, exists := extraBody["contents"]; exists {
			mapped, err := compatibleContentsOverride(ctx, override, contents)
			if err != nil {
				return nil, err
			}
			// Both static overrides and the pinned SDK's Gemini/Vertex
			// converters now supply the same representation to restoration.
			extraBody["contents"] = mapped
		}
	}

	return &genai.HTTPOptions{
		ExtraBody: extraBody,
		ExtrasRequestProvider: func(body map[string]any) map[string]any {
			// Automatic continuation and stream reconnects reuse this closure
			// and ExtraBody. The SDK merge can alias its nested values, so
			// snapshot the freshly generated body on every hook invocation.
			body = cloneRequestBody(body)
			// The SDK deep-marshals request structs through float64 before
			// backend conversion. Restore exact stored model parts afterward.
			wireContents := body["contents"].([]map[string]any) //nolint:errcheck // Pinned SDK shape or preflight-normalized override.
			for index, parts := range modelParts {
				wireContents[index]["parts"] = cloneRequestValue(parts)
			}
			// Caller policy is final: never restore over its customization.
			// This no-error-return SDK seam cannot validate arbitrary changes
			// made here; callers own any resulting replay/dispatch changes.
			if clientOptions.ExtrasRequestProvider != nil {
				return clientOptions.ExtrasRequestProvider(body)
			}
			return body
		},
	}, nil
}

// cloneRequestBody isolates a normalized request tree without re-encoding it.
// In particular, json.Number values and the SDK's []map[string]any shapes survive.
func cloneRequestBody(body map[string]any) map[string]any {
	cloned := maps.Clone(body)
	for key, value := range cloned {
		cloned[key] = cloneRequestValue(value)
	}
	return cloned
}

func cloneRequestValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneRequestBody(value)
	case []map[string]any:
		cloned := slices.Clone(value)
		for index, entry := range cloned {
			cloned[index] = cloneRequestBody(entry)
		}
		return cloned
	case []any:
		cloned := slices.Clone(value)
		for index, entry := range cloned {
			cloned[index] = cloneRequestValue(entry)
		}
		return cloned
	default:
		return value
	}
}

// compatibleContentsOverride validates the external override after JSON decoding.
// A compatible override preserves every turn's position and role. Model parts
// may be omitted (restoration supplies them) or semantically equal to the stored
// parts; conflicting parts are rejected, not silently overwritten. Other fields
// and user parts remain caller-owned.
func compatibleContentsOverride(ctx context.Context, override any, contents []*genai.Content) ([]map[string]any, error) {
	entries, ok := override.([]any)
	if !ok || len(entries) != len(contents) {
		return nil, ErrIncompatibleContentsOverride
	}
	mapped := make([]map[string]any, len(entries))
	for index, entry := range entries {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		content, ok := entry.(map[string]any)
		if !ok || content["role"] != contents[index].Role {
			return nil, fmt.Errorf("%w at turn %d", ErrIncompatibleContentsOverride, index)
		}
		if parts, exists := content["parts"]; exists && contents[index].Role == string(genai.RoleModel) {
			data, err := json.Marshal(contents[index].Parts)
			if err != nil {
				return nil, fmt.Errorf("%w at turn %d", ErrIncompatibleContentsOverride, index)
			}
			var native any
			if err := decodeReplayJSON(data, &native); err != nil || !equalReplayJSON(ctx, parts, native) {
				return nil, fmt.Errorf("%w at turn %d", ErrIncompatibleContentsOverride, index)
			}
		}
		mapped[index] = content
	}
	return mapped, nil
}

// runStream consumes one SDK attempt. accepted means at least one event was
// synchronously accepted by downstream; false from downstream is returned
// separately and must terminate the invocation silently.
func (p *Provider) runStream(
	ctx context.Context,
	contents []*genai.Content,
	config *genai.GenerateContentConfig,
	stream bool,
	yield func(llm.CompletionChunk, error) bool,
) (llm.Usage, bool, llm.FinishReason, []llm.ToolCall, bool, error, bool) {
	var usage llm.Usage
	var usageReported bool
	finishReason := llm.FinishReasons.UNKNOWN
	callsByID := make(map[string]llm.ToolCall)
	var order []string
	partIndex := 0
	accepted := false

	for resp, err := range p.responses(ctx, contents, config, stream) {
		if cause := context.Cause(ctx); cause != nil {
			return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted, cause, true
		}
		if err != nil {
			return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted, err, true
		}
		if resp.UsageMetadata != nil {
			usage = llm.Usage{
				PromptTokens:            int(resp.UsageMetadata.PromptTokenCount),
				CompletionTokens:        int(resp.UsageMetadata.CandidatesTokenCount) + int(resp.UsageMetadata.ThoughtsTokenCount),
				TotalTokens:             int(resp.UsageMetadata.TotalTokenCount),
				CachedTokens:            int(resp.UsageMetadata.CachedContentTokenCount),
				ReasoningTokens:         int(resp.UsageMetadata.ThoughtsTokenCount),
				ReasoningTokensReported: resp.UsageMetadata.ThoughtsTokenCount > 0,
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
			if part != nil && (part.InlineData != nil || part.FileData != nil) {
				return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted,
					fmt.Errorf("google: %w: media output is unsupported", llm.ErrModelNotSupported), true
			}
			if part == nil {
				continue
			}
			downstream, err := emitPart(part, partIndex, callsByID, &order, &accepted, yield)
			if err != nil {
				return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted, err, true
			}
			if !downstream {
				return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted, nil, false
			}
			partIndex++
		}
	}
	return usage, usageReported, finishReason, orderedCalls(callsByID, order), accepted, nil, true
}

// responses keeps both native operations lazy and feeds the same response codec.
// GenerateContent owns JSON decoding and response-body cleanup in the SDK; a
// single-shot response is not re-encoded or passed through an SSE decoder.
func (p *Provider) responses(ctx context.Context, contents []*genai.Content, config *genai.GenerateContentConfig, stream bool) iter.Seq2[*genai.GenerateContentResponse, error] {
	if stream {
		return p.client.Models.GenerateContentStream(ctx, p.model, contents, config)
	}
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		response, err := p.client.Models.GenerateContent(ctx, p.model, contents, config)
		yield(response, err)
	}
}

func emitPart(
	part *genai.Part,
	outputIndex int,
	callsByID map[string]llm.ToolCall,
	order *[]string,
	accepted *bool,
	yield func(llm.CompletionChunk, error) bool,
) (bool, error) {
	data, err := json.Marshal(part)
	if err != nil {
		return true, fmt.Errorf("google: encode continuation part: %w", err)
	}
	if string(data) == "{}" {
		return true, nil
	}
	item := llm.ContinuationItem{
		OutputIndex: llm.OutputPosition(outputIndex),
		Provider:    continuationProvider,
		Format:      continuationFormat,
		Kind:        "part",
		Data:        data,
	}
	chunk := llm.CompletionChunk{CompletedItems: []llm.ContinuationItem{item}}
	if part.Thought {
		chunk.Thinking = part.Text
	} else {
		chunk.Content = part.Text
		if part.Text != "" {
			chunk.ContentOutputIndex = llm.OutputPosition(outputIndex)
		}
	}
	if part.FunctionCall != nil {
		id := part.FunctionCall.ID
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, len(*order))
		}
		argBytes, err := json.Marshal(part.FunctionCall.Args)
		if err != nil {
			return true, fmt.Errorf("google: encode function arguments: %w", err)
		}
		if _, exists := callsByID[id]; exists {
			return true, errors.New("google: ambiguous native function-call ID")
		}
		*order = append(*order, id)
		callsByID[id] = llm.ToolCall{
			ID:          id,
			Type:        "function",
			OutputIndex: llm.OutputPosition(outputIndex),
			Function: llm.ToolCallFunction{
				Name:      part.FunctionCall.Name,
				Arguments: string(argBytes),
			},
		}
		chunk.CompletedItems[0].ID = id
	}
	// A part and its display projection are accepted together, so an early
	// downstream stop cannot retain text without the native replay data.
	if !yield(chunk, nil) {
		return false, nil
	}
	*accepted = true
	return true, nil
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
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
		},
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
	return func(p *Provider) { p.model = model }
}

// WithClient injects a fully-configured genai client; the constructors
// then skip their own client construction entirely, so the client's
// backend, credentials, and endpoint win — and NewProvider's apiKey may
// be empty. This is the escape hatch for configurations the
// constructors don't model: explicit Vertex credentials, custom
// transports, or endpoint setups beyond WithBaseURL.
//
// The client's ExtrasRequestProvider runs after exact stored model-part
// restoration and has final authority over the body. Replay guarantees apply
// before that hook; callers own any subsequent history changes. Static
// ExtraBody.contents overrides with model history must retain turn count/roles
// and omit model parts or supply semantically equal parts. Other fields and
// user-only contents overrides are preserved.
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

// WithInteractionsAPI selects stateless Gemini v1beta Interactions instead of
// GenerateContent. False (the default) retains GenerateContent. Selection is
// validated against the actual client when the completion stream is iterated.
// Requests explicitly disable storage and background execution and replay the
// full supplied history. Host Affinity and prompt_cache_key hints do not select
// a server session or guarantee routing. Explicit thinking budgets/levels and
// local chat-template controls are unsupported; returned thoughts are retained.
func WithInteractionsAPI(enabled bool) options.Option[Provider] {
	return func(p *Provider) { p.interactionsAPI = enabled }
}
