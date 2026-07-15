package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func collectResponsesChunks(t *testing.T, sse string) ([]llm.CompletionChunk, error) {
	t.Helper()
	var chunks []llm.CompletionChunk
	err := parseResponsesSSE(strings.NewReader(sse), func(chunk llm.CompletionChunk, err error) bool {
		if err != nil {
			return false
		}
		chunks = append(chunks, chunk)
		return true
	})
	return chunks, err
}

func TestParseResponsesSSE_TextThinkingUsageAndUnknown(t *testing.T) {
	t.Parallel()
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		``,
		`data: {"type":"response.reasoning_text.delta","delta":"think"}`,
		``,
		`data: {"type":"response.unknown","ignored":true}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":3}}}}`,
		``,
	}, "\n")
	chunks, err := collectResponsesChunks(t, sse)
	if err != nil {
		t.Fatalf("parseResponsesSSE: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3: %+v", len(chunks), chunks)
	}
	if chunks[0].Content != "hi" {
		t.Fatalf("content chunk = %+v", chunks[0])
	}
	if chunks[1].Thinking != "think" {
		t.Fatalf("thinking chunk = %+v", chunks[1])
	}
	if !chunks[2].Done || chunks[2].Usage == nil || chunks[2].Usage.CachedTokens != 3 {
		t.Fatalf("done usage chunk = %+v", chunks[2])
	}
}

func TestParseResponsesSSE_FunctionCallsDeterministicOrder(t *testing.T) {
	t.Parallel()
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"item-b","call_id":"call-b","name":"b","arguments":"{\"b\":"}}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","output_index":2,"item_id":"item-b","delta":"1}"}`,
		``,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"item-a","call_id":"call-a","name":"a","arguments":"{}"}}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{}}}`,
		``,
	}, "\n")
	chunks, err := collectResponsesChunks(t, sse)
	if err != nil {
		t.Fatalf("parseResponsesSSE: %v", err)
	}
	last := chunks[len(chunks)-1]
	if len(last.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v, want two", last.ToolCalls)
	}
	if last.ToolCalls[0].Function.Name != "a" || last.ToolCalls[1].Function.Name != "b" {
		t.Fatalf("tool call order = %+v, want output-index order a,b", last.ToolCalls)
	}
	if last.ToolCalls[1].Function.Arguments != `{"b":1}` {
		t.Fatalf("call b arguments = %q", last.ToolCalls[1].Function.Arguments)
	}
}

func TestMessagesToResponsesInput_EmptyToolOutputStillSerialized(t *testing.T) {
	t.Parallel()
	items := messagesToResponsesInput([]llm.Message{{Role: llm.RoleTool, ToolCallID: "call_1"}})
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Output != "(no output)" {
		t.Fatalf("tool output = %q, want placeholder", items[0].Output)
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"output":"(no output)"`) {
		t.Fatalf("payload missing required output: %s", payload)
	}
}

func TestMessagesToResponsesInput_UserPartsIncludeImages(t *testing.T) {
	t.Parallel()
	items := messagesToResponsesInput([]llm.Message{{
		Role: llm.RoleUser,
		Parts: []llm.ContentPart{
			{Type: llm.ContentTypeText, Text: "look"},
			{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,abc", Detail: "high"}},
		},
	}})
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	parts := items[0].Content
	if len(parts) != 2 {
		t.Fatalf("parts = %#v, want text + image", parts)
	}
	if parts[0].Type != "input_text" || parts[0].Text != "look" {
		t.Fatalf("text part = %#v", parts[0])
	}
	if parts[1].Type != "input_image" || parts[1].ImageURL == "" || parts[1].Detail != "high" {
		t.Fatalf("image part = %#v", parts[1])
	}
}

func TestResponsesHTTPErrorRateLimit(t *testing.T) {
	t.Parallel()
	header := http.Header{"Retry-After": []string{"2"}}
	err := responsesHTTPError(http.StatusTooManyRequests, header, []byte(`{"error":{"message":"Rate limit reached. Please try again in 4.081s","code":"rate_limit"}}`), nil)
	var rle *llm.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("responsesHTTPError = %T %v, want RateLimitError", err, err)
	}
	if !rle.Retryable || rle.RetryAfter != 2*time.Second {
		t.Fatalf("rate limit = %+v, want retryable 2s", rle)
	}
}

func TestParseResponsesSSERateLimitFailure(t *testing.T) {
	t.Parallel()
	body := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"error":{"message":"Rate limit reached. Please try again in 19.661s.","code":"rate_limit_exceeded"}}}` + "\n\n"
	var got error
	err := parseResponsesSSE(strings.NewReader(body), func(_ llm.CompletionChunk, err error) bool {
		got = err
		return err == nil
	})
	if err != nil {
		t.Fatalf("parseResponsesSSE returned %v", err)
	}
	var rle *llm.RateLimitError
	if !errors.As(got, &rle) {
		t.Fatalf("stream error = %T %v, want RateLimitError", got, got)
	}
	if !rle.Retryable || rle.Permanent {
		t.Fatalf("rate limit flags = %+v", rle)
	}
	if rle.RetryAfter != 19661*time.Millisecond {
		t.Fatalf("RetryAfter = %v, want 19.661s", rle.RetryAfter)
	}
}

func TestResponsesHTTPErrorOrdinary400(t *testing.T) {
	t.Parallel()
	err := responsesHTTPError(http.StatusBadRequest, nil, []byte(`{"error":{"message":"bad"}}`), nil)
	var rle *llm.RateLimitError
	if errors.As(err, &rle) {
		t.Fatalf("responsesHTTPError ordinary 400 returned rate limit: %+v", rle)
	}
}
