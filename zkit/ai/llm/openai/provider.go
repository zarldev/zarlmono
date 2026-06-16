package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// defaultHTTPClient builds the http.Client we hand to the openai SDK.
// No total-request Timeout — http.Client.Timeout covers reading the
// streaming body, and SSE generations regularly run for minutes
// (DeepSeek reasoner, long tool-call turns). Stream lifetime is
// governed by ctx instead, with transport-level bounds (dial, TLS,
// response-header, idle) catching dead servers via
// [zhttp.DefaultTransport].
func defaultHTTPClient() *http.Client {
	return &http.Client{Transport: zhttp.DefaultTransport()}
}

// Provider implements the OpenAI LLM provider using the official OpenAI Go SDK.
type Provider struct {
	client           openai.Client
	model            string
	apiKey           string
	baseURL          string
	reasoningHistory llm.ReasoningHistory
	// reasoningKeepMask is an optional per-message keep/drop mask
	// applied in llm.ReasoningHistories.FIELD mode. A wrapping provider (e.g.
	// DeepSeek-V4, which requires reasoning_content only inside
	// tool-call windows) installs a function via
	// [WithReasoningKeepMask] to downgrade selected messages from
	// Field to Strip semantics. When nil, every assistant message
	// in Field mode keeps its reasoning_content.
	reasoningKeepMask func([]llm.Message) []bool
}

const (
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

// NewProvider creates a new OpenAI provider with variadic options.
func NewProvider(apiKey string, opts ...options.Option[Provider]) (*Provider, error) {
	if apiKey == "" {
		return nil, llm.ErrInvalidAPIKey
	}

	// Create OpenAI client with default options
	clientOpts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(defaultHTTPClient()),
	}

	client := openai.NewClient(clientOpts...)

	provider := &Provider{
		client:  client,
		model:   "gpt-4o-mini", // Default model
		apiKey:  apiKey,
		baseURL: "https://api.openai.com/v1", // Default base URL
	}

	// Apply options
	for _, opt := range opts {
		opt(provider)
	}

	return provider, nil
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return llm.LLMProviders.OPENAI.String()
}

// Complete generates a completion using OpenAI's API as an iter.Seq2 — the
// stream's chunks are yielded directly (errors as the second value), no
// channel.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if req.Stream {
			p.streamCompletion(ctx, req, yield)
		} else {
			p.nonStreamCompletion(ctx, req, yield)
		}
	}, nil
}

// streamCompletion handles streaming responses using the official OpenAI SDK,
// yielding each chunk (errors as the second value). It stops early if yield
// returns false (the consumer broke / the attempt was cancelled).
func (p *Provider) streamCompletion(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	// Convert our LLM messages to OpenAI messages
	messages := p.convertMessagesToOpenAI(req.Messages)

	// Build OpenAI chat completion parameters using the v2 API.
	//
	// StreamOptions.IncludeUsage is the wire-level switch that asks
	// the server to emit a trailing chunk carrying token usage. Without
	// it, llama.cpp / vLLM / the upstream OpenAI API all return null
	// usage for the entire stream, and the runner's token-pressure
	// compaction gate (zkit/agent/runner/run.go: tokenPressureBudget)
	// stays closed forever because lastUsage.PromptTokens reads 0.
	// The trailing chunk arrives with choices=[]; see the stream loop
	// below for how usage is captured separately from finish_reason.
	params := openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    p.model,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}

	// Set optional parameters
	if req.Temperature > 0 {
		params.Temperature = openai.Float(float64(req.Temperature))
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}

	// Convert tools if present. Always send tool_choice: "auto"
	// explicitly when tools are in play — hosted OpenAI's default IS
	// "auto" so this is a no-op there, but on llama.cpp (whose chat
	// autoparser gates the tool-call grammar on tool_choice != NONE)
	// the explicit value documents intent and survives SDK omission
	// quirks. We deliberately don't use "required" because the runner
	// terminates the loop when the model returns no tool call —
	// forcing one every turn would break that contract.
	if len(req.Tools) > 0 {
		tools := convertToolsToOpenAI(req.Tools)
		params.Tools = tools
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.String("auto"),
		}
	}

	// Create streaming completion. chat_template_kwargs is a
	// llama.cpp / vLLM extension the OpenAI SDK doesn't know about;
	// inject it as an extra top-level JSON field via WithJSONSet.
	// Providers that don't recognise the key ignore it.
	stream := p.client.Chat.Completions.NewStreaming(ctx, params,
		extraJSONOptions(req)...)

	// OpenAI streams tool calls as deltas keyed by Index — the first
	// delta for an index carries the ID + Name; subsequent deltas are
	// argument fragments with empty ID/Name. We track the ID per index
	// here so each forwarded delta carries a stable ID the runner can
	// accumulate against.
	idByIndex := map[int64]string{}

	// llama.cpp / vLLM Qwen-thinking extension: when enable_thinking
	// is on, deltas carry a non-standard reasoning_content field
	// (parallel to content). Route those bytes through the runner's
	// out-of-band Thinking channel — Content stays the visible answer,
	// Thinking carries reasoning, the two stay disjoint.

	// Stream events following the official example pattern.
	//
	// finishReason + finalUsage accumulate across chunks because
	// llama.cpp (and OpenAI under stream_options.include_usage) emit
	// finish_reason on one chunk and usage on a *separate* trailing
	// chunk with choices=[]. The old code returned early on the
	// finish_reason chunk and never observed the usage chunk, so
	// downstream lastUsage was permanently zero — which silently
	// disabled the runner's token-pressure compaction gate. Keep
	// draining until stream.Next() returns false; emit the terminal
	// Done chunk once, after the loop, with whatever usage we saw.
	var finishReason string
	var finalUsage openai.CompletionUsage
	var usageSeen bool

	for stream.Next() {
		evt := stream.Current()

		// Send content as it comes in
		if len(evt.Choices) > 0 {
			choice := evt.Choices[0]

			// Reasoning deltas come first (Qwen-thinking emits the
			// whole reasoning before any answer). Route them through
			// the Thinking channel only — Content stays the visible
			// answer.
			if r := extractReasoningDelta(choice.Delta); r != "" {
				if !yield(llm.CompletionChunk{Thinking: r}, nil) {
					return
				}
			}

			if choice.Delta.Content != "" {
				// FinishReason is captured below and emitted on the single
				// terminal Done chunk only — not on content deltas — so the
				// "final chunk carries the finish reason" contract holds.
				if !yield(llm.CompletionChunk{Content: choice.Delta.Content}, nil) {
					return
				}
			}

			// Forward every tool-call delta unchanged except for ID
			// stabilisation by Index. The runner accumulates Name and
			// Arguments across chunks.
			if len(choice.Delta.ToolCalls) > 0 {
				toolCalls := make([]llm.ToolCall, 0, len(choice.Delta.ToolCalls))
				for _, tc := range choice.Delta.ToolCalls {
					id, ok := idByIndex[tc.Index]
					if tc.ID != "" {
						id = tc.ID
						idByIndex[tc.Index] = id
					} else if !ok {
						// Some local servers (llama.cpp) skip the ID
						// entirely. Synthesize a stable per-index id so
						// subsequent argument-fragment deltas accumulate
						// onto the same call rather than each becoming a
						// separate tool call.
						id = fmt.Sprintf("call_idx_%d", tc.Index)
						idByIndex[tc.Index] = id
					}
					toolCalls = append(toolCalls, llm.ToolCall{
						ID:   id,
						Type: "function",
						Function: llm.ToolCallFunction{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					})
				}
				if !yield(llm.CompletionChunk{ToolCalls: toolCalls}, nil) {
					return
				}
			}

			// Capture the finish_reason but DO NOT exit the loop yet.
			// llama.cpp emits usage in the next chunk (choices=[]); the
			// upstream OpenAI API does the same when include_usage is
			// set. Returning here would race the usage chunk out of
			// existence and starve the token-pressure gate.
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}

		// Usage rides on a trailing chunk with choices=[] under the
		// stream_options.include_usage extension. Some providers also
		// attach it to the final choice-bearing chunk; capture
		// whichever shape arrives. Any non-zero token field counts as
		// "real usage data" — zero across all three means the server
		// either didn't honour include_usage or hasn't emitted it yet.
		if evt.Usage.TotalTokens > 0 || evt.Usage.PromptTokens > 0 || evt.Usage.CompletionTokens > 0 {
			finalUsage = evt.Usage
			usageSeen = true
		}
	}

	if err := stream.Err(); err != nil {
		yield(llm.CompletionChunk{Done: true}, fmt.Errorf("stream: %w", err))
		return
	}

	// One terminal chunk per stream. Usage is attached only when the
	// server actually emitted it (otherwise we'd write zero values
	// into runner.lastUsage and silently re-disable the compaction
	// gate the include_usage flag is supposed to feed).
	done := llm.CompletionChunk{
		Done:         true,
		FinishReason: finishReason,
	}
	if usageSeen {
		done.Usage = &llm.Usage{
			PromptTokens:     int(finalUsage.PromptTokens),
			CompletionTokens: int(finalUsage.CompletionTokens),
			TotalTokens:      int(finalUsage.TotalTokens),
			CachedTokens:     int(finalUsage.PromptTokensDetails.CachedTokens),
		}
	}
	yield(done, nil)
}

// reasoningFieldCandidates is the ordered list of streaming-delta
// JSON field names different OpenAI-compatible servers use to carry
// reasoning content. The official OpenAI o-series reasoning is NOT
// exposed via the chat/completions stream (it's hidden), so anything
// we see here is non-standard:
//
//   - reasoning_content: llama.cpp / vLLM with Qwen-thinking, and
//     OpenAI-compat proxies that mirror that convention.
//   - reasoning: shorter spelling some aggregators (OpenRouter,
//     certain "ChatGPT-API"-style reverse proxies for gpt-5/gpt-5.5)
//     ship instead. Was the leak source the user reported on a
//     "non-API" GPT-5.5 — server emitted reasoning under .reasoning
//     and our extractor only knew .reasoning_content, so it landed
//     verbatim in the visible content channel.
//   - thinking / thought: occasional variants on local servers and
//     wrapper services. Cheap to scan all four; extractor returns on
//     the first non-empty hit.
//
// Order matters only for the "two candidates populated at once"
// edge case where the longer/more specific name wins — in practice
// each server only sets one.
var reasoningFieldCandidates = []string{
	"reasoning_content",
	"reasoning",
	"reasoning_summary",
	"thinking",
	"thought",
}

// extractReasoningDelta pulls the reasoning text from a streaming
// delta. Tries each name in reasoningFieldCandidates against the
// SDK's typed ExtraFields map first; falls back to a single JSON
// parse of the raw chunk when the SDK didn't surface the keys
// (some streaming paths don't populate ExtraFields). Returns "" when
// no candidate is present, null, or fails to decode.
func extractReasoningDelta(delta openai.ChatCompletionChunkChoiceDelta) string {
	for _, name := range reasoningFieldCandidates {
		field, ok := delta.JSON.ExtraFields[name]
		if !ok || !field.Valid() {
			continue
		}
		if v := decodeReasoningField(field.Raw()); v != "" {
			return v
		}
	}
	// SDK didn't surface any candidate via ExtraFields — parse the
	// raw chunk JSON once and probe every name. Cheap; only fires
	// when the model is actually emitting reasoning.
	return probeReasoningFromRaw(delta.RawJSON())
}

// probeReasoningFromRaw scans the raw streaming-chunk JSON for any
// of the recognised reasoning field names. Pulled out of
// extractReasoningDelta so the multi-field detection is testable
// without mocking the openai-go SDK's typed delta.
func probeReasoningFromRaw(raw string) string {
	if raw == "" {
		return ""
	}
	var probe struct {
		ReasoningContent string `json:"reasoning_content"`
		Reasoning        string `json:"reasoning"`
		ReasoningSummary string `json:"reasoning_summary"`
		Thinking         string `json:"thinking"`
		Thought          string `json:"thought"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return ""
	}
	for _, v := range []string{
		probe.ReasoningContent,
		probe.Reasoning,
		probe.ReasoningSummary,
		probe.Thinking,
		probe.Thought,
	} {
		if v != "" {
			return v
		}
	}
	return ""
}

// decodeReasoningField unwraps the JSON-encoded reasoning_content
// value (a raw string literal, e.g. `"some text"`). Returns "" for
// "null", empty, or malformed input.
func decodeReasoningField(raw string) string {
	if raw == "" || raw == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return ""
	}
	return s
}

func extractReasoningFromMessage(msg openai.ChatCompletionMessage) string {
	for _, name := range reasoningFieldCandidates {
		field, ok := msg.JSON.ExtraFields[name]
		if !ok || !field.Valid() {
			continue
		}
		if v := decodeReasoningField(field.Raw()); v != "" {
			return v
		}
	}
	return probeReasoningFromRaw(msg.RawJSON())
}

// nonStreamCompletion handles non-streaming responses using the official OpenAI SDK.
func (p *Provider) nonStreamCompletion(
	ctx context.Context,
	req llm.CompletionRequest,
	yield func(llm.CompletionChunk, error) bool,
) {
	// Convert our LLM messages to OpenAI messages
	messages := p.convertMessagesToOpenAI(req.Messages)

	// Build OpenAI chat completion parameters using the v2 API
	params := openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    p.model,
	}

	// Set optional parameters
	if req.Temperature > 0 {
		params.Temperature = openai.Float(float64(req.Temperature))
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}

	// Convert tools if present. See streaming path for tool_choice
	// rationale — same explicit "auto" applies here.
	if len(req.Tools) > 0 {
		tools := convertToolsToOpenAI(req.Tools)
		params.Tools = tools
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.String("auto"),
		}
	}

	// Create completion (see extraJSONOptions for chat_template_kwargs handling).
	completion, err := p.client.Chat.Completions.New(ctx, params,
		extraJSONOptions(req)...)
	if err != nil {
		yield(llm.CompletionChunk{Done: true}, fmt.Errorf("completion: %w", err))
		return
	}

	if len(completion.Choices) > 0 {
		choice := completion.Choices[0]

		// Convert tool calls if present
		var toolCalls []llm.ToolCall
		for _, tc := range choice.Message.ToolCalls {
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}

		yield(llm.CompletionChunk{
			Content:      choice.Message.Content,
			Thinking:     extractReasoningFromMessage(choice.Message),
			FinishReason: choice.FinishReason,
			Done:         true,
			ToolCalls:    toolCalls,
			Usage: &llm.Usage{
				PromptTokens:     int(completion.Usage.PromptTokens),
				CompletionTokens: int(completion.Usage.CompletionTokens),
				TotalTokens:      int(completion.Usage.TotalTokens),
				CachedTokens:     int(completion.Usage.PromptTokensDetails.CachedTokens),
			},
		}, nil)
	}
}

// extraJSONOptions builds a slice of openai-go RequestOptions that
// inject our custom top-level JSON fields into the request body.
// The OpenAI SDK doesn't model llama.cpp / vLLM extension fields,
// but option.WithJSONSet lets us slip them in via the underlying
// *bytes.Buffer.
//
// Always injects `cache_prompt: true` — llama.cpp's server respects
// it (reuses the KV-cache for any matching prefix, which is a huge
// win across iterations of a turn AND across turns whose system
// prompt + early history is unchanged); hosted OpenAI / vLLM /
// other strict-but-tolerant servers ignore unknown top-level
// fields. Effectively free for non-llama backends, large saving
// for llama backends.
//
// Returns the option slice the callers spread into their
// NewStreaming/New variadics.
func extraJSONOptions(req llm.CompletionRequest) []option.RequestOption {
	opts := []option.RequestOption{
		option.WithJSONSet("cache_prompt", true),
	}
	if len(req.ChatTemplateKwargs) > 0 {
		opts = append(opts, option.WithJSONSet("chat_template_kwargs", req.ChatTemplateKwargs))
	}
	if rf, ok := buildResponseFormat(req.ResponseFormat); ok {
		opts = append(opts, option.WithJSONSet("response_format", rf))
	}
	return opts
}

// buildResponseFormat translates the provider-neutral
// llm.ResponseFormat into the OpenAI-compatible response_format
// payload that hosted OpenAI, llama.cpp, and vLLM all accept on the
// wire. Returns ok=false for the zero value so callers can skip the
// WithJSONSet injection entirely (avoids sending response_format:null,
// which some servers reject).
//
// json_schema is the meat: the inner `schema` field MUST be the raw
// JSON Schema map — wrapping it in any additional envelope (like
// {"$schema": ..., "schema": ...}) confuses llama-server's
// grammar-converter and silently disables constraint. Strict is
// best-effort: OpenAI honours it; llama.cpp's grammar sampler is
// strict by construction and ignores the flag.
func buildResponseFormat(rf llm.ResponseFormat) (map[string]any, bool) {
	switch rf.Type {
	case llm.ResponseFormatJSONObject:
		return map[string]any{"type": "json_object"}, true
	case llm.ResponseFormatJSONSchema:
		if rf.Schema.IsZero() {
			return nil, false
		}
		// The typed Schema, NOT .Map(): Map() flattens to map[string]any
		// and Go marshals map keys alphabetically, destroying
		// Schema.PropertyOrder — the rationale-before-enum document order
		// llama.cpp's grammar converter reads. Marshalling the typed value
		// runs Schema.MarshalJSON, which honours the order.
		inner := map[string]any{
			"name":   rf.Name,
			"schema": rf.Schema,
		}
		if rf.Strict {
			inner["strict"] = true
		}
		return map[string]any{
			"type":        "json_schema",
			"json_schema": inner,
		}, true
	default:
		return nil, false
	}
}

// Option functions for configuring the OpenAI provider

// Conversion functions between our LLM types and OpenAI SDK types

// userMessageWithParts builds a user message whose Content is the
// OpenAI multimodal "array of parts" form. Caller has already checked
// len(msg.Parts) > 0.
func userMessageWithParts(msg llm.Message) openai.ChatCompletionMessageParamUnion {
	parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(msg.Parts))
	for _, p := range msg.Parts {
		switch p.Type {
		case llm.ContentTypeText:
			parts = append(parts, openai.TextContentPart(p.Text))
		case llm.ContentTypeImage:
			if p.Image == nil {
				continue
			}
			url := p.Image.DataURI
			if url == "" {
				url = p.Image.URL
			}
			if url == "" {
				continue
			}
			parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
				URL:    url,
				Detail: p.Image.Detail,
			}))
		case llm.ContentTypeAudio:
			if p.Audio == nil || p.Audio.DataURI == "" {
				continue
			}
			// OpenAI's input-audio expects raw base64, not a data URI;
			// strip the data: prefix if present.
			data := p.Audio.DataURI
			if i := strings.Index(data, ","); strings.HasPrefix(data, "data:") && i >= 0 {
				data = data[i+1:]
			}
			parts = append(
				parts,
				openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
					Data:   data,
					Format: p.Audio.Format,
				}),
			)
		}
	}
	return openai.UserMessage(parts)
}

func assistantMessageParam(msg llm.Message, mode llm.ReasoningHistory) openai.ChatCompletionAssistantMessageParam {
	// Inline re-wraps reasoning into a `<think>…</think>` prefix so
	// Qwen-style local models that need preserve_thinking see their
	// own prior reasoning on the next turn. Field forwards reasoning
	// via the reasoning_content extra field; Strip drops it.
	content := msg.Content
	if mode == llm.ReasoningHistories.INLINE && msg.ReasoningContent != "" {
		content = "<think>" + msg.ReasoningContent + "</think>" + content
	}

	assistant := openai.ChatCompletionAssistantMessageParam{}
	// DeepSeek's strict OpenAI-compat surface rejects assistant
	// messages where neither content nor tool_calls is present
	// ("Invalid assistant message: content or tool_calls must be set").
	// Always emit the content field with at least an empty string
	// when there are no tool calls so the message is wire-valid.
	if content != "" || len(msg.ToolCalls) == 0 {
		assistant.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
			OfString: openai.String(content),
		}
	}
	if mode == llm.ReasoningHistories.FIELD && msg.ReasoningContent != "" {
		assistant.SetExtraFields(map[string]any{"reasoning_content": msg.ReasoningContent})
	}
	return assistant
}

// convertMessagesToOpenAI converts our llm.Message slice to the OpenAI
// param slice using the provider's configured reasoning mode and any
// installed keep-mask hook. The live send path goes through here;
// tests exercise convertMessagesToOpenAIWithReasoning directly.
func (p *Provider) convertMessagesToOpenAI(messages []llm.Message) []openai.ChatCompletionMessageParamUnion {
	return convertMessagesToOpenAIWithReasoning(messages, p.reasoningHistory, p.reasoningKeepMask)
}

// convertMessagesToOpenAIWithReasoning serializes messages for the
// wire. keepMaskFn is the optional Field-mode trim hook: when set and
// mode is llm.ReasoningHistories.FIELD, the returned mask says which assistant
// messages keep reasoning_content; the others are downgraded to Strip
// semantics. nil keepMaskFn = every Field-mode assistant message
// keeps its reasoning.
func convertMessagesToOpenAIWithReasoning(
	messages []llm.Message,
	mode llm.ReasoningHistory,
	keepMaskFn func([]llm.Message) []bool,
) []openai.ChatCompletionMessageParamUnion {
	var keep []bool
	if mode == llm.ReasoningHistories.FIELD && keepMaskFn != nil {
		keep = keepMaskFn(messages)
	}

	var result []openai.ChatCompletionMessageParamUnion

	for i, msg := range messages {
		switch msg.Role {
		case "system":
			result = append(result, openai.SystemMessage(msg.Content))
		case roleUser:
			if len(msg.Parts) > 0 {
				result = append(result, userMessageWithParts(msg))
				continue
			}
			result = append(result, openai.UserMessage(msg.Content))
		case roleAssistant:
			msgMode := mode
			if mode == llm.ReasoningHistories.FIELD && keep != nil && !keep[i] {
				msgMode = llm.ReasoningHistories.STRIP
			}
			if len(msg.ToolCalls) == 0 {
				assistant := assistantMessageParam(msg, msgMode)
				result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
				continue
			}
			// Build an assistant message that carries tool_calls so the
			// model sees its own prior calls in history.
			toolCalls := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					},
				})
			}
			assistant := assistantMessageParam(msg, msgMode)
			assistant.ToolCalls = toolCalls
			result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
		case roleTool:
			result = append(result, openai.ToolMessage(msg.Content, msg.ToolCallID))
		default:
			result = append(result, openai.UserMessage(msg.Content))
		}
	}

	return result
}

// convertToolsToOpenAI converts our llm.Tool slice to OpenAI tool parameters using the v2 API.
func convertToolsToOpenAI(tools []llm.Tool) []openai.ChatCompletionToolUnionParam {
	var result []openai.ChatCompletionToolUnionParam

	for _, tool := range tools {
		result = append(result, openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        tool.Function.Name,
			Description: openai.String(tool.Function.Description),
			Parameters:  tool.Function.ParametersMap(),
		}))
	}

	return result
}

// WithBaseURL sets a custom base URL for the OpenAI API.
func WithBaseURL(baseURL string) options.Option[Provider] {
	return func(p *Provider) {
		if baseURL != "" {
			p.baseURL = baseURL
			// Recreate the client with the new base URL and existing API key
			clientOpts := []option.RequestOption{
				option.WithAPIKey(p.apiKey),
				option.WithBaseURL(baseURL),
				option.WithHTTPClient(defaultHTTPClient()),
			}
			p.client = openai.NewClient(clientOpts...)
		}
	}
}

// WithTimeout sets a custom whole-request timeout for HTTP requests.
// Streaming callers should usually leave this unset and rely on ctx
// cancellation instead — http.Client.Timeout covers reading the
// streaming body, so a value short enough to be useful for non-stream
// calls will truncate long SSE generations mid-flight. When set, the
// transport still uses [zhttp.DefaultTransport]'s connect / TLS /
// response-header timeouts.
func WithTimeout(timeout time.Duration) options.Option[Provider] {
	return func(p *Provider) {
		if timeout > 0 {
			clientOpts := []option.RequestOption{
				option.WithAPIKey(p.apiKey),
				option.WithHTTPClient(&http.Client{
					Transport: zhttp.DefaultTransport(),
					Timeout:   timeout,
				}),
			}
			if p.baseURL != "" {
				clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
			}
			p.client = openai.NewClient(clientOpts...)
		}
	}
}

// WithReasoningHistory sets how prior-turn assistant reasoning is
// serialized into request history. See [llm.ReasoningHistory] for the modes.
func WithReasoningHistory(mode llm.ReasoningHistory) options.Option[Provider] {
	return func(p *Provider) {
		p.reasoningHistory = mode
	}
}

// WithReasoningKeepMask installs a per-message hook that decides which
// assistant turns retain their reasoning_content in [llm.ReasoningHistories.FIELD]
// mode. The function is called once per request with the full history;
// it returns a parallel []bool where false downgrades that index to
// [llm.ReasoningHistories.STRIP] semantics. nil (the default) keeps every Field-mode
// assistant message.
//
// Intended for backends like DeepSeek-V4 whose API requires
// reasoning_content only inside tool-call windows — the deepseek
// wrapper installs its window-tracking mask here so the trim logic
// stays in the provider that needs it.
func WithReasoningKeepMask(fn func([]llm.Message) []bool) options.Option[Provider] {
	return func(p *Provider) {
		p.reasoningKeepMask = fn
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) options.Option[Provider] {
	return func(p *Provider) {
		if client != nil {
			clientOpts := []option.RequestOption{
				option.WithAPIKey(p.apiKey),
				option.WithHTTPClient(client),
			}
			if p.baseURL != "" {
				clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
			}
			p.client = openai.NewClient(clientOpts...)
		}
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
