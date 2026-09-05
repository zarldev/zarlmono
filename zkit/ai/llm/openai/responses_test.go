package openai_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func responsesTool() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{Name: "read", Parameters: llm.SchemaFromMap(map[string]any{"type": "object"})}}
}

func runResponses(t *testing.T, handler http.Handler, messages []llm.Message) ([]llm.CompletionChunk, error) {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	p, err := openai.NewProvider("test-key", openai.WithBaseURL(server.URL), openai.WithResponsesAPI(true), openai.WithModel("gpt-5.6-sol"))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	var chunks []llm.CompletionChunk
	var streamErr error
	for chunk, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: messages, Tools: []llm.Tool{responsesTool()}}) {
		if err != nil {
			streamErr = err
			continue
		}
		chunks = append(chunks, chunk)
	}
	return chunks, streamErr
}

func TestProviderParsesResponsesEvents(t *testing.T) {
	t.Parallel()
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"think\"}\n\n" +
		"data: {\"type\":\"response.unknown\",\"ignored\":true}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15,\"input_tokens_details\":{\"cached_tokens\":3}}}}\n\n"
	chunks, err := runResponses(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(chunks) != 3 || chunks[0].Content != "hi" || chunks[1].Thinking != "think" {
		t.Fatalf("chunks = %+v, want text, thinking, metadata", chunks)
	}
	if !chunks[2].UsageReported || chunks[2].Usage.CachedTokens != 3 {
		t.Fatalf("usage chunk = %+v", chunks[2])
	}
}

func TestProviderOrdersResponsesFunctionCalls(t *testing.T) {
	t.Parallel()
	sse := "data: {\"type\":\"response.output_item.added\",\"output_index\":2,\"item\":{\"type\":\"function_call\",\"id\":\"item-b\",\"call_id\":\"call-b\",\"name\":\"b\",\"arguments\":\"{\\\"b\\\":\"}}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":2,\"item_id\":\"item-b\",\"delta\":\"1}\"}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"item-a\",\"call_id\":\"call-a\",\"name\":\"a\",\"arguments\":\"{}\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"
	chunks, err := runResponses(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	last := chunks[len(chunks)-1]
	if len(last.ToolCalls) != 2 || last.ToolCalls[0].Function.Name != "a" || last.ToolCalls[1].Function.Name != "b" || last.ToolCalls[1].Function.Arguments != `{"b":1}` {
		t.Fatalf("tool calls = %+v", last.ToolCalls)
	}
}

func TestProviderSerializesResponsesInput(t *testing.T) {
	t.Parallel()
	var body map[string]any
	messages := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "call_1"},
		{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "look"}, {Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,abc", Detail: "high"}}}},
	}
	_, err := runResponses(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
			t.Errorf("decode request: %v", decodeErr)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}), messages)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	input := body["input"].([]any)
	toolOutput := input[0].(map[string]any)
	if toolOutput["output"] != "(no output)" {
		t.Fatalf("tool output = %#v", toolOutput)
	}
	parts := input[1].(map[string]any)["content"].([]any)
	if parts[0].(map[string]any)["type"] != "input_text" || parts[1].(map[string]any)["type"] != "input_image" || parts[1].(map[string]any)["detail"] != "high" {
		t.Fatalf("parts = %#v", parts)
	}
}

func TestProviderClassifiesResponsesErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body, retryAfter string
		status                 int
		wantRate               bool
		wantDelay              time.Duration
	}{
		{name: "http rate limit", status: http.StatusTooManyRequests, retryAfter: "2", body: `{"error":{"message":"Rate limit reached. Please try again in 4.081s","code":"rate_limit"}}`, wantRate: true, wantDelay: 2 * time.Second},
		{name: "stream rate limit", status: http.StatusOK, body: "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"Rate limit reached. Please try again in 19.661s.\",\"code\":\"rate_limit_exceeded\"}}}\n\n", wantRate: true, wantDelay: 19661 * time.Millisecond},
		{name: "ordinary 400", status: http.StatusBadRequest, body: `{"error":{"message":"bad"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := runResponses(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
				} else {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				_, _ = w.Write([]byte(tc.body))
			}), nil)
			var rate *llm.RateLimitError
			if got := errors.As(err, &rate); got != tc.wantRate {
				t.Fatalf("error = %T %v, rate=%v want %v", err, err, got, tc.wantRate)
			}
			if tc.wantRate && (!rate.Retryable || rate.Permanent || rate.RetryAfter != tc.wantDelay) {
				t.Fatalf("rate limit = %+v", rate)
			}
		})
	}
}
