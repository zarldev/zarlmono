package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

const defaultModel = anthropic.ModelClaudeSonnet4_6

// Provider implements the Anthropic Claude LLM provider using the official SDK.
type Provider struct {
	client  *anthropic.Client
	model   anthropic.Model
	apiKey  string
	baseURL string // optional override; empty → SDK default
}

// NewProvider creates a new Anthropic SDK provider with variadic options.
//
// Options are applied to the Provider struct (model, baseURL, etc.)
// BEFORE the SDK client is constructed, so a baseURL override
// supplied via WithBaseURL takes effect on every request. Earlier
// shape applied options after client construction, then tried to
// rebuild the client inside WithBaseURL — but the option saw
// client==nil and was a no-op. The whole baseURL-redirect path
// (used by conformance tests + by any consumer wanting to point at
// a private proxy) silently fell through to the public endpoint.
func NewProvider(apiKey string, opts ...options.Option[Provider]) (*Provider, error) {
	if apiKey == "" {
		return nil, llm.ErrInvalidAPIKey
	}

	provider := &Provider{
		model:  defaultModel,
		apiKey: apiKey,
	}

	// Apply options FIRST so client construction sees the final
	// baseURL / model / etc.
	for _, opt := range opts {
		opt(provider)
	}

	clientOpts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if provider.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(provider.baseURL))
	}
	client := anthropic.NewClient(clientOpts...)
	provider.client = &client

	return provider, nil
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return llm.LLMProviders.ANTHROPIC.String()
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

// streamCompletion handles streaming responses, yielding each chunk (errors
// as the second value). It stops early if yield returns false (the consumer
// broke / the attempt was cancelled).
func (p *Provider) streamCompletion(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	messages := convertMessagesToSDK(req.Messages)

	// Build request parameters
	params := anthropic.MessageNewParams{
		Model:     p.model,
		Messages:  messages,
		MaxTokens: int64(getMaxTokens(req.MaxTokens)),
	}

	// Set temperature if not zero
	// Extended thinking forbids a custom temperature (the API requires the
	// default), so only forward it when thinking is off.
	if req.Temperature > 0 && !req.Thinking.Enabled {
		params.Temperature = anthropic.Float(float64(req.Temperature))
	}

	// Add system prompt if present. Mark with cache_control:ephemeral
	// so Anthropic serves the (typically multi-kilobyte) system
	// prompt from its KV cache on subsequent turns, saving prompt
	// tokens on every iteration within a 5-minute window. The cost
	// of a cache write is ~25% extra on the first request; the
	// payoff is ~90% off on every cached read after that.
	systemPrompt := extractSystemPrompt(req.Messages)
	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{
				Text:         systemPrompt,
				CacheControl: anthropic.NewCacheControlEphemeralParam(),
			},
		}
	}

	// Add tools if present
	if len(req.Tools) > 0 {
		params.Tools = convertToolsToSDK(req.Tools)
	}

	applyResponseFormat(&params, req.ResponseFormat)
	applyThinking(&params, req.Thinking)

	slog.InfoContext(ctx, "sending streaming request to anthropic sdk",
		"model", p.model,
		"messages_count", len(messages),
		"has_system", systemPrompt != "",
		"tools_count", len(req.Tools))

	// Create streaming request
	stream := p.client.Messages.NewStreaming(ctx, params)

	// Track accumulated message for tool calls and usage
	message := anthropic.Message{}

	// Process stream events
	for stream.Next() {
		event := stream.Current()

		// Accumulate the message
		if err := message.Accumulate(event); err != nil {
			yield(llm.CompletionChunk{Done: true}, fmt.Errorf("accumulate stream event: %w", err))
			return
		}

		// Handle different event types using type switch
		switch eventVariant := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			// Message started
			slog.DebugContext(ctx, "message started")

		case anthropic.ContentBlockStartEvent:
			// Check if this is a tool use block by checking the Type field
			if eventVariant.ContentBlock.Type == "tool_use" {
				// Tool use starting - just log it, we'll send the complete tool call at ContentBlockStopEvent
				slog.DebugContext(ctx, "tool use block started",
					"id", eventVariant.ContentBlock.ID,
					"name", eventVariant.ContentBlock.Name,
					"index", eventVariant.Index)
			}

		case anthropic.ContentBlockDeltaEvent:
			// Handle both text and tool input streaming
			switch deltaVariant := eventVariant.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if deltaVariant.Text != "" {
					if !yield(llm.CompletionChunk{
						Content: deltaVariant.Text,
					}, nil) {
						return
					}
				}
			case anthropic.ThinkingDelta:
				// Extended-thinking reasoning stream → Thinking chunk, which
				// the runner splits from visible content for the cockpit's
				// reasoning pane.
				if deltaVariant.Thinking != "" {
					if !yield(llm.CompletionChunk{
						Thinking: deltaVariant.Thinking,
					}, nil) {
						return
					}
				}
			case anthropic.InputJSONDelta:
				// Tool input is being streamed - just log it, we'll send complete tool at ContentBlockStopEvent
				if deltaVariant.PartialJSON != "" {
					slog.DebugContext(ctx, "tool input delta", "partial", deltaVariant.PartialJSON)
				}
			}

		case anthropic.MessageDeltaEvent:
			// Handle stop sequences
			if eventVariant.Delta.StopSequence != "" {
				slog.DebugContext(ctx, "stop sequence reached", "sequence", eventVariant.Delta.StopSequence)
			}
			// Usage is accumulated in the message

		case anthropic.ContentBlockStopEvent:
			// Content block completed
			slog.InfoContext(ctx, "content block completed", "index", eventVariant.Index, "total_blocks", len(message.Content))
			// Check if this was a tool use block that just completed
			if eventVariant.Index < int64(len(message.Content)) {
				block := message.Content[eventVariant.Index]
				slog.InfoContext(ctx, "checking block type", "type", block.Type)
				if toolBlock, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
					// Send final complete tool call with full arguments
					inputJSON := "{}"
					if len(toolBlock.Input) > 0 {
						inputJSON = string(toolBlock.Input)
					}
					slog.InfoContext(ctx, "tool use completed",
						"id", toolBlock.ID,
						"name", toolBlock.Name,
						"args", inputJSON)
					if !yield(llm.CompletionChunk{
						ToolCalls: []llm.ToolCall{{
							ID:   toolBlock.ID,
							Type: "function",
							Function: llm.ToolCallFunction{
								Name:      toolBlock.Name,
								Arguments: inputJSON,
							},
						}},
					}, nil) {
						return
					}
				}
			}

		case anthropic.MessageStopEvent:
			// Message complete
			slog.DebugContext(ctx, "stream complete")
		}
	}

	if err := stream.Err(); err != nil {
		// Cancellation is expected, not an error — drop to Debug so an
		// active TUI's stdout-tee'd slog handler doesn't paint
		// "ERROR stream error" over the alt-screen on every Ctrl-C.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			slog.DebugContext(ctx, "anthropic stream cancelled", "error", err)
		} else {
			slog.ErrorContext(ctx, "stream error", "error", err)
		}
		// Terminal error path. Done:true so downstream readers see one
		// canonical terminal chunk; the error rides the second yield value.
		yield(llm.CompletionChunk{
			Done: true,
		}, anthropicRateLimitError(err, fmt.Errorf("stream error: %w", err)))
		return
	}

	// Send final chunk with usage. Anthropic splits prompt tokens
	// into base (InputTokens) + cache-creation + cache-read; sum
	// them for the "true" prompt total and surface the cache-read
	// subset so the cockpit can show "served from cache" alongside
	// the gauge.
	var usage *llm.Usage
	if message.Usage.InputTokens > 0 || message.Usage.OutputTokens > 0 ||
		message.Usage.CacheReadInputTokens > 0 || message.Usage.CacheCreationInputTokens > 0 {
		prompt := int(message.Usage.InputTokens +
			message.Usage.CacheCreationInputTokens +
			message.Usage.CacheReadInputTokens)
		usage = &llm.Usage{
			PromptTokens:     prompt,
			CompletionTokens: int(message.Usage.OutputTokens),
			TotalTokens:      prompt + int(message.Usage.OutputTokens),
			CachedTokens:     int(message.Usage.CacheReadInputTokens),
		}
	}

	yield(llm.CompletionChunk{
		Done:         true,
		Usage:        usage,
		FinishReason: string(message.StopReason),
	}, nil)
}

// nonStreamCompletion handles non-streaming responses.
func (p *Provider) nonStreamCompletion(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	messages := convertMessagesToSDK(req.Messages)

	// Build request parameters
	params := anthropic.MessageNewParams{
		Model:     p.model,
		Messages:  messages,
		MaxTokens: int64(getMaxTokens(req.MaxTokens)),
	}

	// Set temperature if not zero
	// Extended thinking forbids a custom temperature (the API requires the
	// default), so only forward it when thinking is off.
	if req.Temperature > 0 && !req.Thinking.Enabled {
		params.Temperature = anthropic.Float(float64(req.Temperature))
	}

	// Add system prompt if present, marked for ephemeral caching
	// (see streaming path for rationale).
	systemPrompt := extractSystemPrompt(req.Messages)
	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{
				Text:         systemPrompt,
				CacheControl: anthropic.NewCacheControlEphemeralParam(),
			},
		}
	}

	// Add tools if present
	if len(req.Tools) > 0 {
		params.Tools = convertToolsToSDK(req.Tools)
	}

	applyResponseFormat(&params, req.ResponseFormat)
	applyThinking(&params, req.Thinking)

	slog.InfoContext(ctx, "sending non-streaming request to anthropic sdk",
		"model", p.model,
		"messages_count", len(messages),
		"has_system", systemPrompt != "",
		"tools_count", len(req.Tools))

	// Make the request
	response, err := p.client.Messages.New(ctx, params)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			slog.DebugContext(ctx, "anthropic non-stream cancelled", "error", err)
		} else {
			slog.ErrorContext(ctx, "anthropic sdk error", "error", err)
		}
		// Terminal error path. Done:true so downstream readers see one
		// canonical terminal chunk; the error rides the second yield value.
		yield(llm.CompletionChunk{
			Done: true,
		}, anthropicRateLimitError(err, fmt.Errorf("anthropic sdk: %w", err)))
		return
	}

	// Extract content, reasoning, and tool calls
	var fullContent strings.Builder
	var thinkingContent strings.Builder
	var toolCalls []llm.ToolCall

	for _, block := range response.Content {
		switch content := block.AsAny().(type) {
		case anthropic.TextBlock:
			fullContent.WriteString(content.Text)

		case anthropic.ThinkingBlock:
			thinkingContent.WriteString(content.Thinking)

		case anthropic.ToolUseBlock:
			// Convert tool use to our format
			inputJSON := "{}"
			if len(content.Input) > 0 {
				// The input is a json.RawMessage
				inputJSON = string(content.Input)
			}

			toolCalls = append(toolCalls, llm.ToolCall{
				ID:   content.ID,
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      content.Name,
					Arguments: inputJSON,
				},
			})
		}
	}

	// Send content, reasoning, and tool calls
	if !yield(llm.CompletionChunk{
		Content:      fullContent.String(),
		Thinking:     thinkingContent.String(),
		ToolCalls:    toolCalls,
		FinishReason: string(response.StopReason),
	}, nil) {
		return
	}

	// Send completion with usage. Combine base prompt + cache
	// creation + cache read into PromptTokens; surface cache reads
	// in CachedTokens so the cockpit can render "served from cache".
	usage := &llm.Usage{}
	if response.Usage.InputTokens > 0 || response.Usage.OutputTokens > 0 ||
		response.Usage.CacheReadInputTokens > 0 || response.Usage.CacheCreationInputTokens > 0 {
		usage.PromptTokens = int(response.Usage.InputTokens +
			response.Usage.CacheCreationInputTokens +
			response.Usage.CacheReadInputTokens)
		usage.CompletionTokens = int(response.Usage.OutputTokens)
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		usage.CachedTokens = int(response.Usage.CacheReadInputTokens)
	}

	yield(llm.CompletionChunk{
		Done:  true,
		Usage: usage,
	}, nil)
}

// extractSystemPrompt extracts and combines system messages.
func extractSystemPrompt(messages []llm.Message) string {
	var systemPrompts []string
	for _, msg := range messages {
		if msg.Role == "system" && strings.TrimSpace(msg.Content) != "" {
			systemPrompts = append(systemPrompts, strings.TrimSpace(msg.Content))
		}
	}
	if len(systemPrompts) > 0 {
		return strings.Join(systemPrompts, "\n\n")
	}
	return ""
}

// convertMessagesToSDK converts our messages to SDK format, preserving
// the tool-call/tool-result pairing the agentic loop depends on.
//
// Anthropic's wire shape differs from the OpenAI-flavoured llm.Message:
// an assistant tool call is a tool_use content block on the ASSISTANT
// message, and its result is a tool_result block on the following USER
// message (keyed by the tool_use id). The runner emits one llm.Message
// per tool result (role "tool"); consecutive results from a single batch
// are coalesced into one user message so user/assistant turns alternate
// as the API requires. Dropping any of this — the previous behaviour,
// which emitted only text blocks — left Claude blind to its own prior
// tool calls, so it re-issued them every turn or the API rejected the
// unpaired tool_use.
func convertMessagesToSDK(messages []llm.Message) []anthropic.MessageParam {
	var sdkMessages []anthropic.MessageParam
	var pendingUser []anthropic.ContentBlockParamUnion

	flushUser := func() {
		if len(pendingUser) > 0 {
			sdkMessages = append(sdkMessages, anthropic.NewUserMessage(pendingUser...))
			pendingUser = nil
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleSystem:
			// Handled separately via extractSystemPrompt.
			continue

		case llm.RoleUser:
			if msg.Content != "" {
				pendingUser = append(pendingUser, anthropic.NewTextBlock(msg.Content))
			}

		case llm.RoleTool:
			// A tool result rides on a user message as a tool_result block,
			// keyed by the assistant tool_use id it answers.
			pendingUser = append(pendingUser,
				anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false))

		case llm.RoleAssistant:
			// Flush the accumulated user/tool group before the assistant
			// turn so roles alternate.
			flushUser()

			var blocks []anthropic.ContentBlockParamUnion
			if msg.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				// Input is marshalled by the SDK at send time; pass the raw
				// arguments JSON so it embeds verbatim rather than being
				// double-encoded as a string. Default to {} when empty —
				// Anthropic requires an object.
				input := json.RawMessage(strings.TrimSpace(tc.Function.Arguments))
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks,
					anthropic.NewToolUseBlock(tc.ID, input, tc.Function.Name))
			}
			// Anthropic rejects an empty assistant message — skip a turn
			// that carried neither visible text nor a tool call.
			if len(blocks) > 0 {
				sdkMessages = append(sdkMessages, anthropic.NewAssistantMessage(blocks...))
			}
		}
	}
	flushUser()

	return sdkMessages
}

// applyResponseFormat maps the provider-neutral ResponseFormat onto
// Anthropic's native structured-output config. A JSON-schema format
// constrains the response to the supplied schema via OutputConfig.Format
// (the platform's structured-outputs feature) — so enum/verdict schemas are
// honored rather than silently ignored. Anthropic has no schemaless "JSON
// object" mode, so ResponseFormatJSONObject and the zero value are no-ops.
func applyResponseFormat(params *anthropic.MessageNewParams, rf llm.ResponseFormat) {
	if rf.Type != llm.ResponseFormatJSONSchema || rf.Schema.IsZero() {
		return
	}
	params.OutputConfig = anthropic.OutputConfigParam{
		Format: anthropic.JSONOutputFormatParam{Schema: rf.Schema.Map()},
	}
}

// applyThinking enables Anthropic extended thinking when the request asks
// for it. The API requires budget_tokens >= 1024 and max_tokens >
// budget_tokens, so the budget is clamped up and max_tokens grown to keep
// headroom. (The caller separately drops a custom temperature, which
// extended thinking forbids.) A disabled config is a no-op, leaving the
// model's default.
func applyThinking(params *anthropic.MessageNewParams, tc llm.ThinkingConfig) {
	if !tc.Enabled {
		return
	}
	budget := max(int64(tc.BudgetTokens), 1024)
	if params.MaxTokens <= budget {
		params.MaxTokens = budget + 4096
	}
	params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
}

// convertToolsToSDK converts our tool format to SDK format.
func convertToolsToSDK(tools []llm.Tool) []anthropic.ToolUnionParam {
	sdkTools := make([]anthropic.ToolUnionParam, len(tools))

	for i, tool := range tools {
		// Create the tool parameter
		toolParam := anthropic.ToolParam{
			Name:        tool.Function.Name,
			Description: anthropic.String(tool.Function.Description),
		}

		// Set up the input schema from the typed parameters.
		if !tool.Function.Parameters.IsZero() {
			inputSchema := anthropic.ToolInputSchemaParam{
				Required: tool.Function.Parameters.Required,
			}
			// Properties marshals via Schema.MarshalJSON; the SDK accepts any.
			if props := tool.Function.Parameters.Properties; props != nil {
				inputSchema.Properties = props
			}
			toolParam.InputSchema = inputSchema
		}

		// Wrap in the union type
		sdkTools[i] = anthropic.ToolUnionParam{
			OfTool: &toolParam,
		}
	}

	return sdkTools
}

// getMaxTokens returns a default if not specified.
func getMaxTokens(maxTokens int) int {
	if maxTokens == 0 {
		return 4096
	}
	return maxTokens
}

// Option functions for configuring the Anthropic provider

// WithBaseURL sets a custom base URL for the Anthropic API. The
// option records the value on the Provider; NewProvider reads it
// when constructing the SDK client (the SDK requires the base URL
// at construction). Empty string leaves the SDK default in place.
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

// WithAnthropicModel sets the model using the SDK's typed model constants.
func WithAnthropicModel(model anthropic.Model) options.Option[Provider] {
	return func(p *Provider) {
		p.model = model
	}
}

// anthropicRateLimitError checks whether err is an Anthropic 429 and, if so,
// returns a *llm.RateLimitError with RetryAfter / ResetAt parsed from the
// response headers. Otherwise returns fallback unchanged.
func anthropicRateLimitError(err error, fallback error) error {
	apiErr, ok := errors.AsType[*anthropic.Error](err)
	if !ok || apiErr.StatusCode != http.StatusTooManyRequests {
		return fallback
	}
	rle := &llm.RateLimitError{
		Message: fmt.Sprintf("anthropic: %s", http.StatusText(apiErr.StatusCode)),
	}
	if resp := apiErr.Response; resp != nil {
		rle.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		rle.ResetAt = parseRateLimitReset(resp.Header.Get("X-Ratelimit-Reset"))
	}
	return rle
}

// parseRetryAfter parses the Retry-After header value which is either
// a decimal number of seconds or an HTTP-date (RFC 1123).
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if d, err := strconv.Atoi(v); err == nil && d > 0 {
		return time.Duration(d) * time.Second
	}
	if t, err := time.Parse(time.RFC1123, v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// parseRateLimitReset parses the X-RateLimit-Reset header as a Unix
// timestamp (integer seconds).
func parseRateLimitReset(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
