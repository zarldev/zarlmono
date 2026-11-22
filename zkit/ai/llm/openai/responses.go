package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/responses"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ResponsesError retains the structured code and HTTP status of a Responses
// failure. StatusCode is zero for in-band failures without an HTTP error status.
// The wrapped diagnostic preserves native rate-limit details; gateway consumers
// must sanitize it before exposing errors from an untrusted endpoint.
type ResponsesError struct {
	// Code is the error code decoded from the JSON error object, or empty if absent.
	Code string
	// StatusCode is the HTTP error status, or zero for an in-band failure.
	StatusCode int
	cause      error
}

func (e *ResponsesError) Error() string { return e.cause.Error() }

// Unwrap preserves native diagnostic and rate-limit inspection.
func (e *ResponsesError) Unwrap() error { return e.cause }

// responseEventType is a typed string for SSE event types. Unknown values
// are ignored for forward compatibility.
type responseEventType string

const (
	responsesTypeMessage             = "message"
	responsesTypeReasoning           = "reasoning"
	responsesTypeFunctionCall        = "function_call"
	responsesTypeFunctionCallOutput  = "function_call_output"
	responsesContentInputText        = "input_text"
	responsesContentOutputText       = "output_text"
	responsesContentInputImage       = "input_image"
	responsesIncompleteContentFilter = "content_filter"
	openAIContinuationProvider       = "openai"
	openAIResponsesItemFormat        = "responses.output_item"
)

const (
	responseEventOutputTextDelta           responseEventType = "response.output_text.delta"
	responseEventReasoningDelta            responseEventType = "response.reasoning.delta"
	responseEventReasoningTextDelta        responseEventType = "response.reasoning_text.delta"
	responseEventReasoningSummaryDelta     responseEventType = "response.reasoning_summary.delta"
	responseEventReasoningSummaryTextDelta responseEventType = "response.reasoning_summary_text.delta"
	responseEventOutputItemAdded           responseEventType = "response.output_item.added"
	responseEventOutputItemDone            responseEventType = "response.output_item.done"
	responseEventFunctionCallArgsDelta     responseEventType = "response.function_call_arguments.delta"
	responseEventFunctionCallArgsDone      responseEventType = "response.function_call_arguments.done"
	responseEventCompleted                 responseEventType = "response.completed"
	responseEventFailed                    responseEventType = "response.failed"
	responseEventIncomplete                responseEventType = "response.incomplete"
	responseEventError                     responseEventType = "error"
)

type responsesRequest struct {
	PromptCacheOptions   *nativeCacheOptionsWire `json:"prompt_cache_options,omitempty"`
	Model                string                  `json:"model"`
	Input                []responsesInputItem    `json:"input"`
	Tools                []responsesTool         `json:"tools,omitempty"`
	ToolChoice           string                  `json:"tool_choice,omitempty"`
	Stream               bool                    `json:"stream"`
	Store                bool                    `json:"store"`
	Reasoning            map[string]string       `json:"reasoning,omitempty"`
	Text                 *responsesText          `json:"text,omitempty"`
	MaxOutputTokens      int                     `json:"max_output_tokens,omitempty"`
	Include              []string                `json:"include,omitempty"`
	ParallelToolCalls    bool                    `json:"parallel_tool_calls,omitempty"`
	PromptCacheKey       string                  `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string                  `json:"prompt_cache_retention,omitempty"`
	ServiceTier          string                  `json:"service_tier,omitempty"`
}

type responsesText struct {
	Verbosity string         `json:"verbosity,omitempty"`
	Format    map[string]any `json:"format,omitempty"`
}

func responsesOutputFormat(rf llm.ResponseFormat) map[string]any {
	switch rf.Type {
	case llm.ResponseFormatJSONObject:
		return map[string]any{jsonKeyType: "json_object"}
	case llm.ResponseFormatJSONSchema:
		name := rf.Name
		if name == "" {
			name = "response"
		}
		return map[string]any{jsonKeyType: responseFormatJSONSchema, "name": name, "schema": rf.Schema.Map(), "strict": rf.Strict}
	default:
		return nil
	}
}

type responsesInputItem struct {
	Type             string                 `json:"type"`
	ID               string                 `json:"id,omitempty"`
	Role             string                 `json:"role,omitempty"`
	Content          []responsesContentPart `json:"content,omitempty"`
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	CallID           string                 `json:"call_id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Arguments        string                 `json:"arguments,omitempty"`
	Output           string                 `json:"output,omitempty"`
	Raw              json.RawMessage        `json:"-"`
}

func (i responsesInputItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return i.Raw, nil
	}
	type wire responsesInputItem
	return json.Marshal(wire(i))
}

type responsesContentPart struct {
	PromptCacheBreakpoint *nativeCacheBreakpointWire `json:"-"`
	Type                  string                     `json:"type"`
	Text                  string                     `json:"text,omitempty"`
	ImageURL              string                     `json:"image_url,omitempty"`
	Detail                string                     `json:"detail,omitempty"`
}

// MarshalJSON adds request-only markers without teaching native replay/output
// decoding to interpret opaque marker values that were previously ignored.
func (part responsesContentPart) MarshalJSON() ([]byte, error) {
	type wire responsesContentPart
	return json.Marshal(struct {
		wire
		PromptCacheBreakpoint *nativeCacheBreakpointWire `json:"prompt_cache_breakpoint,omitempty"`
	}{wire: wire(part), PromptCacheBreakpoint: part.PromptCacheBreakpoint})
}

type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type responseEventEnvelope struct {
	Type responseEventType `json:"type"`
}

type responseTextDelta struct {
	Delta       string `json:"delta"`
	OutputIndex int    `json:"output_index"`
}

type responseReasoningDelta struct {
	Delta string `json:"delta"`
}

type responseOutputItemAdded struct {
	OutputIndex int                `json:"output_index"`
	Item        responseOutputItem `json:"item"`
}

type responseOutputItem struct {
	Type             string                 `json:"type"`
	ID               string                 `json:"id"`
	Role             string                 `json:"role,omitempty"`
	Content          []responsesContentPart `json:"content,omitempty"`
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	CallID           string                 `json:"call_id"`
	Name             string                 `json:"name"`
	Arguments        string                 `json:"arguments"`
	Summary          []responsesContentPart `json:"summary,omitempty"`
	Raw              json.RawMessage        `json:"-"`
}

func (i *responseOutputItem) UnmarshalJSON(data []byte) error {
	type wire responseOutputItem
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*i = responseOutputItem(decoded)
	i.Raw = append(i.Raw[:0], data...)
	return nil
}

type responseArgsDelta struct {
	OutputIndex int    `json:"output_index"`
	ItemID      string `json:"item_id"`
	Delta       string `json:"delta"`
}

type responseArgsDone struct {
	OutputIndex int    `json:"output_index"`
	ItemID      string `json:"item_id"`
	Arguments   string `json:"arguments"`
}

type responseUsage struct {
	rawJSON            []byte
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// UnmarshalJSON preserves legacy typed decoding and retains owned wire bytes
// for presence-aware interpretation of optional native cache-write evidence.
func (u *responseUsage) UnmarshalJSON(data []byte) error {
	type plainUsage responseUsage
	decoded := plainUsage(*u)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*u = responseUsage(decoded)
	u.rawJSON = append([]byte(nil), data...)
	return nil
}

type responseResult struct {
	Status            responses.ResponseStatus            `json:"status"`
	Output            []responseOutputItem                `json:"output"`
	Usage             *responseUsage                      `json:"usage"`
	IncompleteDetails responses.ResponseIncompleteDetails `json:"incomplete_details"`
	Error             *responses.ResponseError            `json:"error"`
}

type responseCompleted struct {
	Response responseResult `json:"response"`
}

func hasOpenAIContinuationItems(messages []llm.Message) bool {
	for _, message := range messages {
		for _, item := range message.ContinuationItems {
			if item.Provider == "openai" && item.Format == "responses.output_item" {
				return true
			}
		}
	}
	return false
}

func openAIContinuationItem(index int, item responseOutputItem) (llm.ContinuationItem, bool) {
	valid := item.Type == responsesTypeReasoning && item.EncryptedContent != "" ||
		item.Type == responsesTypeMessage && item.Role == roleAssistant && hasResponsesOutputText(item.Content)
	if !valid {
		return llm.ContinuationItem{}, false
	}
	return llm.ContinuationItem{OutputIndex: llm.OutputPosition(index), Provider: openAIContinuationProvider, Format: openAIResponsesItemFormat, Kind: item.Type, ID: item.ID, Data: append([]byte(nil), item.Raw...)}, true
}

func hasResponsesOutputText(parts []responsesContentPart) bool {
	for _, part := range parts {
		if part.Type == responsesContentOutputText {
			return true
		}
	}
	return false
}

type responseFailed struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Response struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	} `json:"response"`
}

type pendingResponseToolCall struct {
	id        string
	name      string
	arguments strings.Builder
}

func (p *Provider) responsesCompletion(ctx context.Context, req llm.CompletionRequest, plan responsesPlan, yield func(llm.CompletionChunk, error) bool) {
	input, err := messagesToResponsesInput(req.Messages, plan.nativeCache)
	if err != nil {
		yield(llm.CompletionChunk{}, err)
		return
	}
	body := responsesRequest{
		PromptCacheOptions:   nativeCacheResponsesOptions(plan.nativeCache),
		Model:                p.model,
		Input:                input,
		Tools:                toolsToResponsesTools(req.Tools),
		Stream:               req.Stream,
		Store:                false,
		ParallelToolCalls:    plan.parallelToolCalls,
		PromptCacheKey:       plan.promptCacheKey,
		PromptCacheRetention: plan.promptCacheRetention,
		ServiceTier:          requestServiceTier(req.Options),
	}
	format := responsesOutputFormat(req.ResponseFormat)
	if plan.textVerbosity != "" || format != nil {
		body.Text = &responsesText{Verbosity: plan.textVerbosity, Format: format}
	}
	if len(body.Tools) > 0 {
		body.ToolChoice = "auto"
	}
	if plan.includeEncryptedReasoning {
		body.Include = []string{"reasoning.encrypted_content"}
	}
	if plan.reasoning != nil {
		body.Reasoning = map[string]string{"effort": plan.reasoning.String()}
	}
	if req.MaxTokens > 0 {
		body.MaxOutputTokens = req.MaxTokens
	}

	payload, err := json.Marshal(body)
	if err != nil {
		yield(llm.CompletionChunk{}, fmt.Errorf("responses marshal: %w", err))
		return
	}

	url := strings.TrimRight(p.baseURL, "/") + "/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		yield(llm.CompletionChunk{}, fmt.Errorf("responses request: %w", normalizeContextError(ctx, err)))
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		yield(llm.CompletionChunk{}, fmt.Errorf("responses post: %w", normalizeContextError(ctx, err)))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		yield(llm.CompletionChunk{}, normalizeContextError(ctx, responsesHTTPError(resp.StatusCode, resp.Header, msg, resp.Request)))
		return
	}
	if !req.Stream {
		if err := parseResponsesJSON(ctx, resp.Body, p.nativeOpenAIUsage(), yield); err != nil {
			yield(llm.CompletionChunk{}, normalizeContextError(ctx, err))
		}
		return
	}
	if err := parseResponsesSSE(resp.Body, p.nativeOpenAIUsage(), yield); err != nil {
		yield(llm.CompletionChunk{}, normalizeContextError(ctx, err))
	}
}

// parseResponsesJSON consumes a single native response, not a synthetic event
// stream. Decode and validate the whole document before exposing any output.
func parseResponsesJSON(ctx context.Context, r io.Reader, nativeUsage bool, yield func(llm.CompletionChunk, error) bool) error {
	var result responseResult
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("responses decode: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("responses trailing data: %w", err)
		}
		return errors.New("responses decode: multiple JSON values")
	}
	finish, err := responsesJSONFinish(result)
	if err != nil {
		return err
	}
	for index, item := range result.Output {
		if err := validateResponsesJSONItem(item); err != nil {
			return fmt.Errorf("responses output %d: %w", index, err)
		}
	}
	for index, item := range result.Output {
		if err := contextError(ctx); err != nil {
			return err
		}
		chunk := llm.CompletionChunk{}
		switch item.Type {
		case responsesTypeMessage:
			var text strings.Builder
			for _, part := range item.Content {
				if part.Type == responsesContentOutputText {
					text.WriteString(part.Text)
				}
			}
			chunk.Content = text.String()
			if chunk.Content != "" {
				chunk.ContentOutputIndex = llm.OutputPosition(index)
			}
		case responsesTypeReasoning:
			var thinking strings.Builder
			for _, part := range item.Summary {
				if part.Type == "summary_text" {
					thinking.WriteString(part.Text)
				}
			}
			// Prefer the summary projection; raw reasoning content remains in
			// the native item and is used only when no summary was supplied.
			if thinking.Len() == 0 {
				for _, part := range item.Content {
					if part.Type == "reasoning_text" {
						thinking.WriteString(part.Text)
					}
				}
			}
			chunk.Thinking = thinking.String()
		case responsesTypeFunctionCall:
			id := item.CallID
			if id == "" {
				id = item.ID
			}
			chunk.ToolCalls = []llm.ToolCall{{ID: id, Type: typeFunction, OutputIndex: llm.OutputPosition(index), Function: llm.ToolCallFunction{Name: item.Name, Arguments: item.Arguments}}}
		}
		if native, ok := openAIContinuationItem(index, item); ok {
			chunk.CompletedItems = []llm.ContinuationItem{native}
		}
		if chunk.Content != "" || chunk.Thinking != "" || len(chunk.CompletedItems) > 0 || len(chunk.ToolCalls) > 0 {
			if !yield(chunk, nil) {
				return nil
			}
		}
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	chunk := llm.CompletionChunk{FinishReason: finish}
	setResponsesUsage(&chunk, result.Usage, nativeUsage)
	yield(chunk, nil)
	return nil
}

func responsesJSONFinish(result responseResult) (llm.FinishReason, error) {
	if result.Error != nil {
		return llm.FinishReason{}, responsesFailure(string(result.Error.Code), result.Error.Message)
	}
	switch result.Status {
	case responses.ResponseStatusCompleted, responses.ResponseStatusIncomplete:
		if result.Output == nil {
			return llm.FinishReason{}, errors.New("responses decode: missing output")
		}
	case responses.ResponseStatusFailed:
		return llm.FinishReason{}, responsesFailure("", "")
	default:
		return llm.FinishReason{}, fmt.Errorf("responses decode: unexpected status %q", result.Status)
	}
	if result.Status == responses.ResponseStatusIncomplete {
		switch result.IncompleteDetails.Reason {
		case "max_output_tokens", "max_messages":
			return llm.FinishReasons.LENGTH, nil
		case responsesIncompleteContentFilter:
			return llm.FinishReasons.CONTENTFILTER, nil
		case "":
			return llm.FinishReason{}, errors.New("responses decode: incomplete response without reason")
		default:
			return llm.FinishReasons.UNKNOWN, nil
		}
	}
	for _, item := range result.Output {
		if item.Type == responsesTypeFunctionCall {
			return llm.FinishReasons.TOOLCALLS, nil
		}
	}
	return llm.FinishReasons.STOP, nil
}

func validateResponsesJSONItem(item responseOutputItem) error {
	switch item.Type {
	case "":
		return errors.New("missing item type")
	case responsesTypeMessage:
		var message responses.ResponseOutputMessage
		if err := json.Unmarshal(item.Raw, &message); err != nil {
			return fmt.Errorf("message decode: %w", err)
		}
		if item.ID == "" || item.Role != roleAssistant || item.Content == nil {
			return errors.New("invalid assistant message")
		}
		if message.Status != responses.ResponseOutputMessageStatusCompleted && message.Status != responses.ResponseOutputMessageStatusIncomplete {
			return fmt.Errorf("unexpected message status %q", message.Status)
		}
		for _, part := range message.Content {
			if part.Type == "" || part.Type == responsesContentOutputText && !part.JSON.Text.Valid() {
				return errors.New("invalid message content")
			}
		}
	case responsesTypeReasoning:
		var reasoning responses.ResponseReasoningItem
		if err := json.Unmarshal(item.Raw, &reasoning); err != nil {
			return fmt.Errorf("reasoning decode: %w", err)
		}
		if reasoning.Status != "" && reasoning.Status != responses.ResponseReasoningItemStatusCompleted && reasoning.Status != responses.ResponseReasoningItemStatusIncomplete {
			return fmt.Errorf("unexpected reasoning status %q", reasoning.Status)
		}
		if reasoning.ID == "" || reasoning.Summary == nil {
			return errors.New("invalid reasoning item")
		}
		for _, part := range reasoning.Summary {
			if !part.JSON.Text.Valid() {
				return errors.New("invalid reasoning summary")
			}
		}
		for _, part := range reasoning.Content {
			if !part.JSON.Text.Valid() {
				return errors.New("invalid reasoning content")
			}
		}
	case responsesTypeFunctionCall:
		var call responses.ResponseFunctionToolCall
		if err := json.Unmarshal(item.Raw, &call); err != nil {
			return fmt.Errorf("function_call decode: %w", err)
		}
		if call.Status != "" && call.Status != responses.ResponseFunctionToolCallStatusCompleted {
			return fmt.Errorf("unexpected function_call status %q", call.Status)
		}
		if item.CallID == "" && item.ID == "" || strings.TrimSpace(item.Name) == "" {
			return errors.New("function_call without ID or name")
		}
		var arguments map[string]json.RawMessage
		if err := json.Unmarshal([]byte(item.Arguments), &arguments); err != nil {
			return fmt.Errorf("function_call arguments: %w", err)
		}
		if arguments == nil {
			return errors.New("function_call arguments must be an object")
		}
	default:
		return fmt.Errorf("unsupported item type %q", item.Type)
	}
	return nil
}

func setResponsesUsage(chunk *llm.CompletionChunk, usage *responseUsage, nativeUsage bool) {
	if usage == nil {
		return
	}
	chunk.UsageReported = true
	chunk.Usage = llm.Usage{
		PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens,
		TotalTokens: usage.TotalTokens, CachedTokens: usage.InputTokensDetails.CachedTokens,
	}
	if reasoning := usage.OutputTokensDetails.ReasoningTokens; reasoning != nil {
		chunk.Usage.ReasoningTokens = *reasoning
		chunk.Usage.ReasoningTokensReported = true
	}
	if nativeUsage {
		chunk.PricingUsage = nativeResponsesCacheUsage(usage.rawJSON)
		if chunk.PricingUsage != nil && chunk.PricingUsage.Input.CacheWrite.Known {
			chunk.Usage.CacheCreationTokens = int(chunk.PricingUsage.Input.CacheWrite.Tokens)
		}
	}
}

func responsesFailure(code, message string) error {
	if message == "" {
		message = "responses failed"
	}
	if isRateLimitBody(message) || isRateLimitBody(code) {
		rle := &llm.RateLimitError{
			Message:    "responses failed: " + message,
			Retryable:  true,
			RetryAfter: parseRetryAfterFromProse(message),
		}
		if isPermanentQuotaBody(message) || isPermanentQuotaBody(code) {
			rle.Permanent = true
			rle.Retryable = false
		}
		return &ResponsesError{Code: code, cause: rle}
	}
	return &ResponsesError{Code: code, cause: fmt.Errorf("responses failed: %s", message)}
}

func messagesToResponsesInput(messages []llm.Message, policy *nativeCachePolicy) ([]responsesInputItem, error) {
	out := make([]responsesInputItem, 0, len(messages))
	for source, msg := range nativeCacheMessageSources(messages) {
		switch msg.Role {
		case "tool":
			// output is required for function_call_output items. Keep it
			// non-empty so omitempty cannot remove the field and strict
			// Responses endpoints do not reject a void-returning tool.
			output := msg.Content
			if output == "" {
				output = "(no output)"
			}
			out = append(out, responsesInputItem{Type: responsesTypeFunctionCallOutput, CallID: msg.ToolCallID, Output: output})
		case roleAssistant:
			type indexed struct {
				index int
				order int
				item  responsesInputItem
			}
			var items []indexed
			order := 0
			hasNativeText := false
			for _, continuation := range msg.ContinuationItems {
				if continuation.Provider != openAIContinuationProvider || continuation.Format != openAIResponsesItemFormat {
					continue
				}
				var item responsesInputItem
				if json.Unmarshal(continuation.Data, &item) != nil {
					continue
				}
				validReasoning := item.Type == responsesTypeReasoning && item.EncryptedContent != ""
				validText := item.Type == responsesTypeMessage && item.Role == roleAssistant && hasResponsesOutputText(item.Content) && continuation.OutputIndex != nil
				if !validReasoning && !validText {
					continue
				}
				if policy != nil && nativeCacheReplayMarker(continuation.Data) {
					return nil, invalidCachePolicy("native replay cache marker")
				}
				item.Raw = append(item.Raw[:0], continuation.Data...)
				index := order
				if continuation.OutputIndex != nil {
					index = *continuation.OutputIndex
				}
				items = append(items, indexed{index: index, order: order, item: item})
				hasNativeText = hasNativeText || validText
				order++
			}
			if msg.Content != "" && !hasNativeText {
				index := 0
				if msg.ContentOutputIndex != nil {
					index = *msg.ContentOutputIndex
				}
				items = append(items, indexed{index: index, order: order, item: responsesInputItem{Type: responsesTypeMessage, Role: roleAssistant, Content: []responsesContentPart{{Type: responsesContentOutputText, Text: msg.Content}}}})
				order++
			}
			for _, tc := range msg.ToolCalls {
				index := order
				if tc.OutputIndex != nil {
					index = *tc.OutputIndex
				}
				items = append(items, indexed{index: index, order: order, item: responsesInputItem{Type: responsesTypeFunctionCall, CallID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments}})
				order++
			}
			sort.SliceStable(items, func(i, j int) bool {
				if items[i].index == items[j].index {
					return items[i].order < items[j].order
				}
				return items[i].index < items[j].index
			})
			for _, item := range items {
				out = append(out, item.item)
			}
		default:
			role := msg.Role
			if role == "" {
				role = roleUser
			}
			out = append(out, responsesInputItem{Type: responsesTypeMessage, Role: role, Content: responsesUserContentParts(msg, source, policy)})
		}
	}
	return out, nil
}

func responsesUserContentParts(msg llm.Message, source int, policy *nativeCachePolicy) []responsesContentPart {
	if len(msg.Parts) == 0 {
		return []responsesContentPart{{Type: responsesContentInputText, Text: msg.Content, PromptCacheBreakpoint: nativeCacheResponsesMarker(policy, source, 0)}}
	}
	parts := make([]responsesContentPart, 0, len(msg.Parts))
	for index, part := range msg.Parts {
		switch part.Type {
		case llm.ContentTypeText:
			parts = append(parts, responsesContentPart{Type: responsesContentInputText, Text: part.Text, PromptCacheBreakpoint: nativeCacheResponsesMarker(policy, source, index)})
		case llm.ContentTypeImage:
			if part.Image == nil {
				continue
			}
			url := part.Image.DataURI
			if url == "" {
				url = part.Image.URL
			}
			if url == "" {
				continue
			}
			parts = append(parts, responsesContentPart{Type: responsesContentInputImage, ImageURL: url, Detail: part.Image.Detail})
		}
	}
	if len(parts) == 0 {
		return []responsesContentPart{{Type: responsesContentInputText, Text: msg.Content}}
	}
	return parts
}

func toolsToResponsesTools(tools []llm.Tool) []responsesTool {
	out := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		out = append(out, responsesTool{Type: typeFunction, Name: tool.Function.Name, Description: tool.Function.Description, Parameters: tool.Function.ParametersMap()})
	}
	return out
}

func parseResponsesSSE(r io.Reader, nativeUsage bool, yield func(llm.CompletionChunk, error) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var data strings.Builder
	calls := map[int]*pendingResponseToolCall{}
	byID := map[string]*pendingResponseToolCall{}
	// orderedIndexes tracks the first-seen order of output indexes so tool
	// calls are emitted deterministically at completion time.
	var orderedIndexes []int
	emittedItems := map[int]bool{}
	flush := func() bool {
		payload := data.String()
		data.Reset()
		if payload == "" || payload == "[DONE]" {
			return false
		}
		return dispatchResponseEvent(payload, calls, byID, &orderedIndexes, emittedItems, nativeUsage, yield)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(after))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("responses sse scan: %w", err)
	}
	if data.Len() > 0 {
		flush()
	}
	return nil
}

func dispatchResponseEvent(payload string, calls map[int]*pendingResponseToolCall, byID map[string]*pendingResponseToolCall, orderedIndexes *[]int, emittedItems map[int]bool, nativeUsage bool, yield func(llm.CompletionChunk, error) bool) bool {
	var env responseEventEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		yield(llm.CompletionChunk{}, fmt.Errorf("responses event: %w", err))
		return true
	}
	switch env.Type {
	case responseEventOutputTextDelta:
		var ev responseTextDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			yield(llm.CompletionChunk{}, err)
			return true
		}
		if ev.Delta != "" {
			return !yield(llm.CompletionChunk{Content: ev.Delta, ContentOutputIndex: llm.OutputPosition(ev.OutputIndex)}, nil)
		}
	case responseEventReasoningDelta, responseEventReasoningTextDelta, responseEventReasoningSummaryDelta, responseEventReasoningSummaryTextDelta:
		var ev responseReasoningDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			yield(llm.CompletionChunk{}, err)
			return true
		}
		if ev.Delta != "" {
			return !yield(llm.CompletionChunk{Thinking: ev.Delta}, nil)
		}
	case responseEventOutputItemAdded, responseEventOutputItemDone:
		var ev responseOutputItemAdded
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			yield(llm.CompletionChunk{}, err)
			return true
		}
		if env.Type == responseEventOutputItemAdded && ev.Item.Type == responsesTypeFunctionCall {
			id := ev.Item.CallID
			if id == "" {
				id = ev.Item.ID
			}
			// Validate that we have a non-empty ID before creating the
			// pending call. Without one, the tool call would be unusable.
			if id == "" {
				yield(llm.CompletionChunk{}, fmt.Errorf("responses event: function_call with no ID at output_index %d", ev.OutputIndex))
				return true
			}
			call := &pendingResponseToolCall{id: id, name: ev.Item.Name}
			call.arguments.WriteString(ev.Item.Arguments)
			calls[ev.OutputIndex] = call
			if ev.Item.ID != "" {
				byID[ev.Item.ID] = call
			}
			// Track first-seen order for deterministic output.
			*orderedIndexes = append(*orderedIndexes, ev.OutputIndex)
		}
		if env.Type == responseEventOutputItemDone && !emittedItems[ev.OutputIndex] {
			if item, ok := openAIContinuationItem(ev.OutputIndex, ev.Item); ok {
				emittedItems[ev.OutputIndex] = true
				return !yield(llm.CompletionChunk{CompletedItems: []llm.ContinuationItem{item}}, nil)
			}
		}
	case responseEventFunctionCallArgsDelta:
		var ev responseArgsDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			yield(llm.CompletionChunk{}, err)
			return true
		}
		call := calls[ev.OutputIndex]
		if call == nil && ev.ItemID != "" {
			call = byID[ev.ItemID]
		}
		if call != nil {
			call.arguments.WriteString(ev.Delta)
		}
	case responseEventFunctionCallArgsDone:
		var ev responseArgsDone
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			yield(llm.CompletionChunk{}, err)
			return true
		}
		call := calls[ev.OutputIndex]
		if call == nil && ev.ItemID != "" {
			call = byID[ev.ItemID]
		}
		if call != nil && ev.Arguments != "" {
			call.arguments.Reset()
			call.arguments.WriteString(ev.Arguments)
		}
	case responseEventCompleted, responseEventIncomplete:
		var ev responseCompleted
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			yield(llm.CompletionChunk{}, err)
			return true
		}
		var nativeItems []llm.ContinuationItem
		for index, output := range ev.Response.Output {
			if emittedItems[index] {
				continue
			}
			if item, ok := openAIContinuationItem(index, output); ok {
				emittedItems[index] = true
				nativeItems = append(nativeItems, item)
			}
		}
		// Emit tool calls in output-index order rather than nondeterministic
		// map iteration.
		indexes := append([]int(nil), (*orderedIndexes)...)
		sort.Ints(indexes)
		toolCalls := make([]llm.ToolCall, 0, len(indexes))
		for _, idx := range indexes {
			call := calls[idx]
			if call == nil || call.name == "" {
				continue
			}
			toolCalls = append(toolCalls, llm.ToolCall{ID: call.id, Type: typeFunction, OutputIndex: llm.OutputPosition(idx), Function: llm.ToolCallFunction{Name: call.name, Arguments: call.arguments.String()}})
		}
		chunk := llm.CompletionChunk{FinishReason: llm.FinishReasons.STOP, ToolCalls: toolCalls, CompletedItems: nativeItems}
		setResponsesUsage(&chunk, ev.Response.Usage, nativeUsage)
		if len(toolCalls) > 0 {
			chunk.FinishReason = llm.FinishReasons.TOOLCALLS
		}
		if env.Type == responseEventIncomplete {
			chunk.FinishReason = llm.FinishReasons.LENGTH
			if ev.Response.IncompleteDetails.Reason == responsesIncompleteContentFilter {
				chunk.FinishReason = llm.FinishReasons.CONTENTFILTER
			}
		}
		yield(chunk, nil)
		return true // A protocol terminal event ends reading even when accepted.
	case responseEventFailed, responseEventError:
		var ev responseFailed
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			yield(llm.CompletionChunk{}, err)
			return true
		}
		if env.Type == responseEventError {
			ev.Response.Error.Code, ev.Response.Error.Message = ev.Code, ev.Message
		}
		yield(llm.CompletionChunk{}, responsesFailure(ev.Response.Error.Code, ev.Response.Error.Message))
		return true
	default:
		// Unknown events are silently ignored for forward compatibility.
	}
	return false
}

// responsesHTTPError normalizes non-2xx Responses API responses into typed
// errors. HTTP 429 or explicit rate-limit/quota body produces
// *llm.RateLimitError with parsed retry timing. Other statuses produce
// contextual ordinary errors.
func responsesHTTPError(statusCode int, header http.Header, body []byte, _ *http.Request) error {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	bodyStr := strings.TrimSpace(string(body))
	if len(bodyStr) > 512 {
		bodyStr = bodyStr[:512] + "... (truncated)"
	}

	// Check for rate-limit conditions: 429 status, or explicit quota/rate
	// limit prose in the body.
	if statusCode == http.StatusTooManyRequests || isRateLimitBody(bodyStr) {
		rle := &llm.RateLimitError{
			Message:   fmt.Sprintf("responses status %d: %s", statusCode, bodyStr),
			Retryable: true,
		}
		// Parse Retry-After header first.
		if ra := header.Get("Retry-After"); ra != "" {
			rle.RetryAfter = parseRetryAfter(ra)
		}
		// Fallback to parsing the body for "try again in N.Ns" prose.
		if rle.RetryAfter == 0 {
			rle.RetryAfter = parseRetryAfterFromProse(bodyStr)
		}
		// Mark permanent quota/billing conditions.
		if isPermanentQuotaBody(bodyStr) {
			rle.Permanent = true
			rle.Retryable = false
		}
		return &ResponsesError{Code: envelope.Error.Code, StatusCode: statusCode, cause: rle}
	}

	return &ResponsesError{Code: envelope.Error.Code, StatusCode: statusCode, cause: fmt.Errorf("responses status %d: %s", statusCode, bodyStr)}
}

// isRateLimitBody reports whether the response body indicates a rate-limit
// or quota condition.
func isRateLimitBody(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "tpm")
}

// isPermanentQuotaBody reports whether the body indicates a hard quota/billing
// condition that retrying won't resolve.
func isPermanentQuotaBody(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "insufficient quota") ||
		strings.Contains(lower, "billing") ||
		strings.Contains(lower, "payment")
}

// parseRetryAfterFromProse extracts a retry-after duration from provider
// prose such as "Please try again in 4.081s".
func parseRetryAfterFromProse(body string) time.Duration {
	if body == "" {
		return 0
	}
	// Match patterns like "try again in 4.081s" or "try again in 4 seconds"
	lower := strings.ToLower(body)
	idx := strings.Index(lower, "try again in ")
	if idx < 0 {
		return 0
	}
	rest := lower[idx+len("try again in "):]
	// Parse the number at the start of rest.
	end := strings.IndexFunc(rest, func(r rune) bool {
		return r != '.' && r != ',' && (r < '0' || r > '9')
	})
	if end <= 0 {
		return 0
	}
	numStr := rest[:end]
	seconds, err := strconv.ParseFloat(numStr, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
