package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// responseEventType is a typed string for SSE event types. Unknown values
// are silently ignored for forward compatibility.
type responseEventType string

const (
	responsesTypeMessage            = "message"
	responsesTypeFunctionCall       = "function_call"
	responsesTypeFunctionCallOutput = "function_call_output"
	responsesContentInputText       = "input_text"
	responsesContentOutputText      = "output_text"
	responsesContentInputImage      = "input_image"
)

const (
	responseEventOutputTextDelta           responseEventType = "response.output_text.delta"
	responseEventReasoningDelta            responseEventType = "response.reasoning.delta"
	responseEventReasoningTextDelta        responseEventType = "response.reasoning_text.delta"
	responseEventReasoningSummaryDelta     responseEventType = "response.reasoning_summary.delta"
	responseEventReasoningSummaryTextDelta responseEventType = "response.reasoning_summary_text.delta"
	responseEventOutputItemAdded           responseEventType = "response.output_item.added"
	responseEventFunctionCallArgsDelta     responseEventType = "response.function_call_arguments.delta"
	responseEventFunctionCallArgsDone      responseEventType = "response.function_call_arguments.done"
	responseEventCompleted                 responseEventType = "response.completed"
	responseEventFailed                    responseEventType = "response.failed"
)

type responsesRequest struct {
	Model             string               `json:"model"`
	Input             []responsesInputItem `json:"input"`
	Tools             []responsesTool      `json:"tools,omitempty"`
	ToolChoice        string               `json:"tool_choice,omitempty"`
	Stream            bool                 `json:"stream"`
	Store             bool                 `json:"store"`
	Reasoning         map[string]string    `json:"reasoning,omitempty"`
	MaxOutputTokens   int                  `json:"max_output_tokens,omitempty"`
	Include           []string             `json:"include,omitempty"`
	ParallelToolCalls bool                 `json:"parallel_tool_calls,omitempty"`
}

type responsesInputItem struct {
	Type      string                 `json:"type"`
	Role      string                 `json:"role,omitempty"`
	Content   []responsesContentPart `json:"content,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	Output    string                 `json:"output,omitempty"`
}

type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
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
	Delta string `json:"delta"`
}

type responseReasoningDelta struct {
	Delta string `json:"delta"`
}

type responseOutputItemAdded struct {
	OutputIndex int                `json:"output_index"`
	Item        responseOutputItem `json:"item"`
}

type responseOutputItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
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

type responseCompleted struct {
	Response struct {
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
}

type responseFailed struct {
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
	body := responsesRequest{
		Model:             p.model,
		Input:             messagesToResponsesInput(req.Messages),
		Tools:             toolsToResponsesTools(req.Tools),
		Stream:            true,
		Store:             false,
		ParallelToolCalls: plan.parallelToolCalls,
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
		yield(llm.CompletionChunk{Done: true}, fmt.Errorf("responses marshal: %w", err))
		return
	}

	url := strings.TrimRight(p.baseURL, "/") + "/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		yield(llm.CompletionChunk{Done: true}, fmt.Errorf("responses request: %w", err))
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := defaultHTTPClient().Do(httpReq)
	if err != nil {
		yield(llm.CompletionChunk{Done: true}, fmt.Errorf("responses post: %w", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		yield(llm.CompletionChunk{Done: true}, responsesHTTPError(resp.StatusCode, resp.Header, msg, resp.Request))
		return
	}
	if err := parseResponsesSSE(resp.Body, yield); err != nil {
		yield(llm.CompletionChunk{Done: true}, err)
	}
}

func messagesToResponsesInput(messages []llm.Message) []responsesInputItem {
	out := make([]responsesInputItem, 0, len(messages))
	for _, msg := range messages {
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
			if msg.Content != "" {
				out = append(out, responsesInputItem{Type: responsesTypeMessage, Role: roleAssistant, Content: []responsesContentPart{{Type: responsesContentOutputText, Text: msg.Content}}})
			}
			for _, tc := range msg.ToolCalls {
				out = append(out, responsesInputItem{Type: responsesTypeFunctionCall, CallID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
			}
		default:
			role := msg.Role
			if role == "" {
				role = roleUser
			}
			out = append(out, responsesInputItem{Type: responsesTypeMessage, Role: role, Content: responsesUserContentParts(msg)})
		}
	}
	return out
}

func responsesUserContentParts(msg llm.Message) []responsesContentPart {
	if len(msg.Parts) == 0 {
		return []responsesContentPart{{Type: responsesContentInputText, Text: msg.Content}}
	}
	parts := make([]responsesContentPart, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch part.Type {
		case llm.ContentTypeText:
			parts = append(parts, responsesContentPart{Type: responsesContentInputText, Text: part.Text})
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

func parseResponsesSSE(r io.Reader, yield func(llm.CompletionChunk, error) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var data strings.Builder
	calls := map[int]*pendingResponseToolCall{}
	byID := map[string]*pendingResponseToolCall{}
	// orderedIndexes tracks the first-seen order of output indexes so tool
	// calls are emitted deterministically at completion time.
	var orderedIndexes []int
	flush := func() bool {
		payload := data.String()
		data.Reset()
		if payload == "" || payload == "[DONE]" {
			return false
		}
		return dispatchResponseEvent(payload, calls, byID, &orderedIndexes, yield)
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
		return nil
	}
	yield(llm.CompletionChunk{Done: true}, nil)
	return nil
}

func dispatchResponseEvent(payload string, calls map[int]*pendingResponseToolCall, byID map[string]*pendingResponseToolCall, orderedIndexes *[]int, yield func(llm.CompletionChunk, error) bool) bool {
	var env responseEventEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return !yield(llm.CompletionChunk{}, fmt.Errorf("responses event: %w", err))
	}
	switch env.Type {
	case responseEventOutputTextDelta:
		var ev responseTextDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, err)
		}
		if ev.Delta != "" {
			return !yield(llm.CompletionChunk{Content: ev.Delta}, nil)
		}
	case responseEventReasoningDelta, responseEventReasoningTextDelta, responseEventReasoningSummaryDelta, responseEventReasoningSummaryTextDelta:
		var ev responseReasoningDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, err)
		}
		if ev.Delta != "" {
			return !yield(llm.CompletionChunk{Thinking: ev.Delta}, nil)
		}
	case responseEventOutputItemAdded:
		var ev responseOutputItemAdded
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, err)
		}
		if ev.Item.Type == responsesTypeFunctionCall {
			id := ev.Item.CallID
			if id == "" {
				id = ev.Item.ID
			}
			// Validate that we have a non-empty ID before creating the
			// pending call. Without one, the tool call would be unusable.
			if id == "" {
				return !yield(llm.CompletionChunk{}, fmt.Errorf("responses event: function_call with no ID at output_index %d", ev.OutputIndex))
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
	case responseEventFunctionCallArgsDelta:
		var ev responseArgsDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, err)
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
			return !yield(llm.CompletionChunk{}, err)
		}
		call := calls[ev.OutputIndex]
		if call == nil && ev.ItemID != "" {
			call = byID[ev.ItemID]
		}
		if call != nil && ev.Arguments != "" {
			call.arguments.Reset()
			call.arguments.WriteString(ev.Arguments)
		}
	case responseEventCompleted:
		var ev responseCompleted
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, err)
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
			toolCalls = append(toolCalls, llm.ToolCall{ID: call.id, Type: typeFunction, Function: llm.ToolCallFunction{Name: call.name, Arguments: call.arguments.String()}})
		}
		chunk := llm.CompletionChunk{Done: true, FinishReason: "stop", ToolCalls: toolCalls}
		if ev.Response.Usage.TotalTokens > 0 || ev.Response.Usage.InputTokens > 0 || ev.Response.Usage.OutputTokens > 0 {
			chunk.Usage = &llm.Usage{PromptTokens: ev.Response.Usage.InputTokens, CompletionTokens: ev.Response.Usage.OutputTokens, TotalTokens: ev.Response.Usage.TotalTokens, CachedTokens: ev.Response.Usage.InputTokensDetails.CachedTokens}
		}
		return !yield(chunk, nil)
	case responseEventFailed:
		var ev responseFailed
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, err)
		}
		msg := ev.Response.Error.Message
		if msg == "" {
			msg = "responses failed"
		}
		if isRateLimitBody(msg) || isRateLimitBody(ev.Response.Error.Code) {
			rle := &llm.RateLimitError{
				Message:   "responses failed: " + msg,
				Retryable: true,
			}
			rle.RetryAfter = parseRetryAfterFromProse(msg)
			if isPermanentQuotaBody(msg) || isPermanentQuotaBody(ev.Response.Error.Code) {
				rle.Permanent = true
				rle.Retryable = false
			}
			return !yield(llm.CompletionChunk{Done: true}, rle)
		}
		return !yield(llm.CompletionChunk{Done: true}, fmt.Errorf("responses failed: %s", msg))
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
		return rle
	}

	return fmt.Errorf("responses status %d: %s", statusCode, bodyStr)
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
