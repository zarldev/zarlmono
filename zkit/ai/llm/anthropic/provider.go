package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

const (
	defaultModel = anthropic.ModelClaudeSonnet4_6

	modelClaudeOpus46   = "claude-opus-4-6"
	modelClaudeOpus47   = "claude-opus-4-7"
	modelClaudeSonnet46 = "claude-sonnet-4-6"

	continuationProvider = "anthropic"
	continuationFormat   = "content_block.v1"
	continuationThinking = "thinking"
	continuationRedacted = "redacted_thinking"
	continuationText     = "text"

	thinkingDisplayOption = "anthropic_thinking_display"
)

// Provider implements the Anthropic Claude LLM provider using the official SDK.
type Provider struct {
	client     *anthropic.Client
	model      anthropic.Model
	apiKey     string
	baseURL    string       // optional override; empty → SDK default
	httpClient *http.Client // optional, borrowed transport policy
}

// NewProvider assembles an Anthropic provider using an already-resolved API key.
// Options are applied before constructing the SDK client; construction performs no I/O.
func NewProvider(apiKey string, opts ...options.Option[Provider]) *Provider {
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
	if provider.httpClient != nil {
		clientOpts = append(clientOpts, option.WithHTTPClient(provider.httpClient))
	}
	client := anthropic.NewClient(clientOpts...)
	provider.client = &client

	return provider
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return llm.LLMProviders.ANTHROPIC.String()
}

// Complete constructs a fully lazy Anthropic completion stream.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if cause := cancellationCause(ctx); cause != nil {
			yield(llm.CompletionChunk{}, cause)
			return
		}
		if req.Stream {
			p.streamCompletion(ctx, req, yield)
		} else {
			p.nonStreamCompletion(ctx, req, yield)
		}
	}
}

// streamCompletion handles streaming responses, yielding each chunk (errors
// as the second value). It stops early if yield returns false (the consumer
// broke / the attempt was cancelled).
func (p *Provider) streamCompletion(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	params, err := p.messageParams(req)
	if err != nil {
		yield(llm.CompletionChunk{}, err)
		return
	}

	// Acquire the SDK stream only after iteration starts and always close it,
	// including cancellation and downstream early-stop paths.
	stream := p.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	// Track accumulated message for tool calls and usage
	message := anthropic.Message{}

	// Process stream events
	for stream.Next() {
		event := stream.Current()

		// Accumulate the message
		if err := message.Accumulate(event); err != nil {
			yield(llm.CompletionChunk{}, completionError(ctx, fmt.Errorf("accumulate stream event: %w", err)))
			return
		}
		// Handle different event types using type switch
		switch eventVariant := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			// Handle both text and tool input streaming
			switch deltaVariant := eventVariant.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if deltaVariant.Text != "" {
					if !yield(llm.CompletionChunk{
						Content:            deltaVariant.Text,
						ContentOutputIndex: llm.OutputPosition(int(eventVariant.Index)),
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
			}

		case anthropic.ContentBlockStopEvent:
			// Emit complete native thinking blocks only once they have accumulated
			// their signature/data. Thinking deltas remain the display projection;
			// the completed item is solely for exact continuation replay.
			if eventVariant.Index < int64(len(message.Content)) {
				block := message.Content[eventVariant.Index]
				if item, ok := anthropicContinuationItem(int(eventVariant.Index), block); ok {
					if !yield(llm.CompletionChunk{CompletedItems: []llm.ContinuationItem{item}}, nil) {
						return
					}
					break
				}
				if toolBlock, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
					// Send final complete tool call with full arguments.
					inputJSON := "{}"
					if len(toolBlock.Input) > 0 {
						inputJSON = string(toolBlock.Input)
					}
					if !yield(llm.CompletionChunk{
						ToolCalls: []llm.ToolCall{{
							ID:          toolBlock.ID,
							OutputIndex: llm.OutputPosition(int(eventVariant.Index)),
							Type:        "function",
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
		}
	}
	if err := stream.Err(); err != nil {
		yield(llm.CompletionChunk{}, completionError(ctx,
			anthropicRateLimitError(err, fmt.Errorf("stream error: %w", err))))
		return
	}

	// Anthropic reports usage on every completed message, including a legitimate
	// all-zero report. Preserve that presence with an owned value.
	usage, err := usageFromAnthropic(message.Usage)
	if err != nil {
		yield(llm.CompletionChunk{}, fmt.Errorf("anthropic usage: %w", err))
		return
	}
	chunk := llm.CompletionChunk{
		FinishReason:  normalizeFinishReason(message.StopReason),
		Usage:         usage,
		UsageReported: true,
	}
	yield(chunk, nil)
}

// nonStreamCompletion handles non-streaming responses.
func (p *Provider) nonStreamCompletion(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	params, err := p.messageParams(req)
	if err != nil {
		yield(llm.CompletionChunk{}, err)
		return
	}

	// Make the request
	response, err := p.client.Messages.New(ctx, params)
	if err != nil {
		yield(llm.CompletionChunk{}, completionError(ctx,
			anthropicRateLimitError(err, fmt.Errorf("anthropic sdk: %w", err))))
		return
	}

	// Extract content, reasoning, and tool calls
	var fullContent strings.Builder
	var thinkingContent strings.Builder
	var toolCalls []llm.ToolCall
	var textIndexes []int
	var completedItems []llm.ContinuationItem

	for index, block := range response.Content {
		if item, ok := anthropicContinuationItem(index, block); ok {
			completedItems = append(completedItems, item)
		}
		switch content := block.AsAny().(type) {
		case anthropic.TextBlock:
			fullContent.WriteString(content.Text)
			textIndexes = append(textIndexes, index)

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
				OutputIndex: llm.OutputPosition(index),
				ID:          content.ID,
				Type:        "function",
				Function: llm.ToolCallFunction{
					Name:      content.Name,
					Arguments: inputJSON,
				},
			})
		}
	}

	// Send content, reasoning, and tool calls
	var contentOutputIndex *int
	if len(textIndexes) > 0 {
		// Anthropic can return multiple text blocks, while Message.Content is a
		// single projection. Replay their byte-concatenation at the first text
		// block's position; this is exact for the usual single or adjacent text
		// blocks but intentionally does not claim separated block boundaries.
		contentOutputIndex = llm.OutputPosition(textIndexes[0])
	}
	if !yield(llm.CompletionChunk{
		Content:            fullContent.String(),
		ContentOutputIndex: contentOutputIndex,
		Thinking:           thinkingContent.String(),
		ToolCalls:          toolCalls,
		CompletedItems:     completedItems,
		FinishReason:       normalizeFinishReason(response.StopReason),
	}, nil) {
		return
	}

	usage, err := usageFromAnthropic(response.Usage)
	if err != nil {
		yield(llm.CompletionChunk{}, fmt.Errorf("anthropic usage: %w", err))
		return
	}
	yield(llm.CompletionChunk{Usage: usage, UsageReported: true}, nil)
}

func usageFromAnthropic(usage anthropic.Usage) (llm.Usage, error) {
	counts := []struct {
		name  string
		value int64
	}{
		{name: "input_tokens", value: usage.InputTokens},
		{name: "output_tokens", value: usage.OutputTokens},
		{name: "cache_creation_input_tokens", value: usage.CacheCreationInputTokens},
		{name: "cache_read_input_tokens", value: usage.CacheReadInputTokens},
	}
	maxInt := int64(^uint(0) >> 1)
	for _, count := range counts {
		if count.value < 0 || count.value > maxInt {
			return llm.Usage{}, fmt.Errorf("invalid %s %d", count.name, count.value)
		}
	}
	if usage.InputTokens > maxInt-usage.CacheCreationInputTokens ||
		usage.InputTokens+usage.CacheCreationInputTokens > maxInt-usage.CacheReadInputTokens {
		return llm.Usage{}, errors.New("prompt token total overflows int")
	}
	prompt := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	if prompt > maxInt-usage.OutputTokens {
		return llm.Usage{}, errors.New("total token count overflows int")
	}

	mapped := llm.Usage{
		PromptTokens:        int(prompt),
		CompletionTokens:    int(usage.OutputTokens),
		TotalTokens:         int(prompt + usage.OutputTokens),
		CachedTokens:        int(usage.CacheReadInputTokens),
		CacheCreationTokens: int(usage.CacheCreationInputTokens),
	}
	if !usage.JSON.CacheCreation.Valid() {
		return mapped, nil
	}
	creation5m := usage.CacheCreation.Ephemeral5mInputTokens
	creation1h := usage.CacheCreation.Ephemeral1hInputTokens
	if creation5m < 0 || creation1h < 0 ||
		creation5m > usage.CacheCreationInputTokens ||
		creation1h > usage.CacheCreationInputTokens-creation5m {
		return mapped, nil
	}
	mapped.CacheCreation5mTokens = int(creation5m)
	mapped.CacheCreation1hTokens = int(creation1h)
	return mapped, nil
}

func cancellationCause(ctx context.Context) error {
	return context.Cause(ctx)
}

func completionError(ctx context.Context, fallback error) error {
	if cause := cancellationCause(ctx); cause != nil {
		return cause
	}
	return fallback
}

func normalizeFinishReason(reason anthropic.StopReason) llm.FinishReason {
	switch reason {
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence, anthropic.StopReasonPauseTurn:
		return llm.FinishReasons.STOP
	case anthropic.StopReasonMaxTokens:
		return llm.FinishReasons.LENGTH
	case anthropic.StopReasonToolUse:
		return llm.FinishReasons.TOOLCALLS
	case anthropic.StopReasonRefusal:
		return llm.FinishReasons.CONTENTFILTER
	default:
		return llm.FinishReasons.UNKNOWN
	}
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

	for _, msg := range llm.ExpandToolResultParts(messages) {
		switch msg.Role {
		case llm.RoleSystem:
			// Handled separately via extractSystemPrompt.
			continue

		case llm.RoleUser:
			appendUserContent(&pendingUser, msg)

		case llm.RoleTool:
			// A tool result rides on a user message as a tool_result block,
			// keyed by the assistant tool_use id it answers.
			pendingUser = append(pendingUser,
				anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false))

		case llm.RoleAssistant:
			// Flush the accumulated user/tool group before the assistant
			// turn so roles alternate.
			flushUser()

			projected := make([]anthropic.ContentBlockParamUnion, 0, 1+len(msg.ToolCalls))
			if msg.Content != "" {
				projected = append(projected, anthropic.NewTextBlock(msg.Content))
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
				projected = append(projected,
					anthropic.NewToolUseBlock(tc.ID, input, tc.Function.Name))
			}
			blocks := mergeAnthropicContinuation(projected, msg.ContinuationItems, msg.Content != "", msg.ContentOutputIndex, msg.ToolCalls)
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

type nativeThinkingBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Citations json.RawMessage `json:"citations,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
}

func anthropicContinuationItem(index int, block anthropic.ContentBlockUnion) (llm.ContinuationItem, bool) {
	var native nativeThinkingBlock
	var raw string
	switch content := block.AsAny().(type) {
	case anthropic.TextBlock:
		native = nativeThinkingBlock{Type: continuationText, Text: content.Text}
		raw = content.RawJSON()
	case anthropic.ThinkingBlock:
		native = nativeThinkingBlock{Type: continuationThinking, Thinking: content.Thinking, Signature: content.Signature}
		raw = content.RawJSON()
	case anthropic.RedactedThinkingBlock:
		native = nativeThinkingBlock{Type: continuationRedacted, Data: content.Data}
		raw = content.RawJSON()
	default:
		return llm.ContinuationItem{}, false
	}
	data, err := json.Marshal(native)
	if err != nil {
		return llm.ContinuationItem{}, false
	}
	// Preserve unknown and omitted fields for consumers that validate exact
	// replay. Reconstructing this block here would hide lossy SDK conversion.
	if raw != "" {
		data = []byte(raw)
	}
	return llm.ContinuationItem{
		OutputIndex: llm.OutputPosition(index),
		Provider:    continuationProvider,
		Format:      continuationFormat,
		Kind:        native.Type,
		Data:        data,
	}, true
}

func mergeAnthropicContinuation(projected []anthropic.ContentBlockParamUnion, items []llm.ContinuationItem, hasContent bool, contentIndex *int, toolCalls []llm.ToolCall) []anthropic.ContentBlockParamUnion {
	type indexedBlock struct {
		index int
		order int
		block anthropic.ContentBlockParamUnion
	}
	indexed := make([]indexedBlock, 0, len(projected)+len(items))
	occupied := make(map[int]bool, len(items))
	hasNativeText := false
	order := 0
	nextNativeIndex := 0
	for _, item := range items {
		if item.Provider != continuationProvider || item.Format != continuationFormat {
			continue
		}
		var block nativeThinkingBlock
		if err := json.Unmarshal(item.Data, &block); err != nil {
			continue
		}
		if item.Kind != "" && item.Kind != block.Type {
			continue
		}
		if block.Type != continuationText && block.Type != continuationThinking && block.Type != continuationRedacted {
			continue
		}
		native := param.Override[anthropic.ContentBlockParamUnion](json.RawMessage(item.Data))
		if block.Type == continuationText {
			hasNativeText = true
		}
		index := nextNativeIndex
		if item.OutputIndex != nil {
			index = *item.OutputIndex
		}
		nextNativeIndex = max(nextNativeIndex, index+1)
		indexed = append(indexed, indexedBlock{index: index, order: order, block: native})
		occupied[index] = true
		order++
	}
	for projectedIndex, block := range projected {
		var position *int
		toolIndex := projectedIndex
		if hasContent {
			if projectedIndex == 0 {
				if hasNativeText {
					continue
				}
				position = contentIndex
			} else {
				toolIndex--
			}
		}
		if position == nil && toolIndex >= 0 && toolIndex < len(toolCalls) {
			position = toolCalls[toolIndex].OutputIndex
		}
		index := 0
		if position != nil {
			index = *position
		} else {
			for occupied[index] {
				index++
			}
		}
		indexed = append(indexed, indexedBlock{index: index, order: order, block: block})
		occupied[index] = true
		order++
	}
	sort.SliceStable(indexed, func(i, j int) bool {
		if indexed[i].index == indexed[j].index {
			return indexed[i].order < indexed[j].order
		}
		return indexed[i].index < indexed[j].index
	})
	blocks := make([]anthropic.ContentBlockParamUnion, len(indexed))
	for i := range indexed {
		blocks[i] = indexed[i].block
	}
	return blocks
}

// setLastMessageCacheBreakpoint marks the final content block of the final
// message with cache_control:ephemeral — a rolling breakpoint that lets
// Anthropic serve the whole conversation prefix from cache on the next turn.
// The tool_result blocks carrying file reads and every prior turn sit before
// it, so on a long agentic loop this is where the real prompt-token savings
// live (the system-prompt breakpoint only covers tools+system, which is a
// fraction of a deep conversation).
//
// Combined with the static system breakpoint this is the standard two-
// breakpoint incremental pattern: each turn writes cache for the newly
// appended suffix and reads the longest matching prefix (everything up to the
// previous turn's breakpoint) from cache. Anthropic allows up to 4
// breakpoints, so two leaves headroom.
//
// Safe on any conversation: an empty message list or a block type that can't
// carry cache_control is a no-op, and a prefix shorter than the model's
// minimum cacheable length is silently ignored by the API rather than erroring.
// staticCacheControl marks the static prefix (tools + system) with a 1-hour
// cache TTL rather than the default 5 minutes. That prefix is identical for a
// whole session, so it is written once and read on every turn; the 1-hour TTL
// lets it survive the multi-minute idle gaps between turns (user think-time,
// tool-approval waits) that would otherwise evict the 5-minute cache and force
// a full re-warm. The extra write cost (2x base vs 1.25x) is paid on that
// one-time write; every read stays ~90% off. The rolling last-message
// breakpoint keeps the cheaper 5-minute default because its tail is superseded
// each turn — a 1-hour write there would pay 2x for content used briefly.
func staticCacheControl() anthropic.CacheControlEphemeralParam {
	cc := anthropic.NewCacheControlEphemeralParam()
	cc.TTL = anthropic.CacheControlEphemeralTTLTTL1h
	return cc
}

func setLastMessageCacheBreakpoint(messages []anthropic.MessageParam) {
	if len(messages) == 0 || len(messages[len(messages)-1].Content) == 0 {
		return
	}
	blocks := messages[len(messages)-1].Content
	index := len(blocks) - 1
	raw, err := json.Marshal(blocks[index])
	if err != nil {
		return
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return
	}
	var kind string
	if json.Unmarshal(object["type"], &kind) != nil || kind == continuationThinking || kind == continuationRedacted {
		return
	}
	if _, exists := object["cache_control"]; exists {
		return
	}
	object["cache_control"] = json.RawMessage(`{"type":"ephemeral"}`)
	blocks[index] = param.Override[anthropic.ContentBlockParamUnion](object)
}

func appendUserContent(blocks *[]anthropic.ContentBlockParamUnion, msg llm.Message) {
	if msg.Parts == nil && msg.Content != "" {
		*blocks = append(*blocks, anthropic.NewTextBlock(msg.Content))
	}
	for _, part := range msg.Parts {
		switch part.Type {
		case llm.ContentTypeText:
			if part.Text != "" {
				*blocks = append(*blocks, anthropic.NewTextBlock(part.Text))
			}
		case llm.ContentTypeImage:
			block, ok := anthropicImageBlock(part.Image)
			if ok {
				*blocks = append(*blocks, block)
			}
		}
	}
}

func anthropicImageBlock(img *llm.ImageData) (anthropic.ContentBlockParamUnion, bool) {
	if img == nil {
		return anthropic.ContentBlockParamUnion{}, false
	}
	if img.DataURI != "" {
		mime, data, ok := splitDataURI(img.DataURI)
		if !ok {
			return anthropic.ContentBlockParamUnion{}, false
		}
		if img.MIMEType != "" {
			mime = img.MIMEType
		}
		return anthropic.NewImageBlockBase64(mime, data), true
	}
	if img.URL != "" {
		return anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: img.URL}), true
	}
	return anthropic.ContentBlockParamUnion{}, false
}

func splitDataURI(uri string) (string, string, bool) {
	const sep = ";base64,"
	prefix, data, ok := strings.Cut(uri, sep)
	if !ok || !strings.HasPrefix(prefix, "data:") || data == "" {
		return "", "", false
	}
	return strings.TrimPrefix(prefix, "data:"), data, true
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
	params.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: rf.Schema.Map()}
}

// applyThinking enables Anthropic extended thinking when the request asks
// for it. The API requires budget_tokens >= 1024 and max_tokens >
// budget_tokens, so the budget is clamped up and max_tokens grown to keep
// headroom. (The caller separately drops a custom temperature, which
// extended thinking forbids.) A disabled config is a no-op, leaving the
// model's default.
func applyThinking(params *anthropic.MessageNewParams, tc llm.ThinkingConfig, modelOptions llm.ModelOptions) {
	if !tc.Enabled {
		return
	}
	display, _ := modelOptions[thinkingDisplayOption].(string)
	validDisplay := display == "summarized" || display == "omitted"
	if adaptiveThinkingModel(params.Model) {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}}
		if validDisplay {
			params.Thinking.OfAdaptive.Display = anthropic.ThinkingConfigAdaptiveDisplay(display)
		}
		return
	}
	budget := max(int64(tc.BudgetTokens), 1024)
	if params.MaxTokens <= budget {
		params.MaxTokens = budget + 4096
	}
	params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
	if validDisplay {
		params.Thinking.OfEnabled.Display = anthropic.ThinkingConfigEnabledDisplay(display)
	}
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
			toolParam.InputSchema = param.Override[anthropic.ToolInputSchemaParam](tool.Function.Parameters)
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
	return func(p *Provider) { p.model = model }
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
		Message:   fmt.Sprintf("anthropic: %s", http.StatusText(apiErr.StatusCode)),
		Retryable: true,
	}
	if resp := apiErr.Response; resp != nil {
		rle.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		rle.ResetAt = parseRateLimitReset(resp.Header.Get("X-Ratelimit-Reset"))
		for _, header := range []string{"Anthropic-Ratelimit-Requests-Reset", "Anthropic-Ratelimit-Tokens-Reset", "Anthropic-Ratelimit-Input-Tokens-Reset", "Anthropic-Ratelimit-Output-Tokens-Reset"} {
			if reset := parseRateLimitReset(resp.Header.Get(header)); reset.After(rle.ResetAt) {
				rle.ResetAt = reset
			}
		}
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

// parseRateLimitReset accepts Anthropic RFC3339 timestamps and proxy Unix resets.
func parseRateLimitReset(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	if instant, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return instant
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
