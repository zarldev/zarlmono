package openai_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestProvider_RecoversLeakedToolCallsNonStreaming(t *testing.T) {
	content := `<tool_calls>[{"id":"c1","name":"read","arguments":{"path":"foo.go"}}]</tool_calls>`
	chunks := completeWithFakeOpenAI(t, false, chatCompletionResponse(content, ""))

	calls := collectToolCalls(chunks)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1; chunks=%#v", len(calls), chunks)
	}
	if calls[0].Function.Name != "read" {
		t.Fatalf("tool name = %q, want read", calls[0].Function.Name)
	}
	if calls[0].Function.Arguments != `{"path":"foo.go"}` {
		t.Fatalf("arguments = %q", calls[0].Function.Arguments)
	}
	if content := collectContent(chunks); content != "" {
		t.Fatalf("visible content = %q, want stripped artifact", content)
	}
}

func TestProvider_NativeToolCallsWinNonStreaming(t *testing.T) {
	body := `{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"<tool_calls>[{\"name\":\"wrong\",\"arguments\":{}}]</tool_calls>","tool_calls":[{"id":"native","type":"function","function":{"name":"read","arguments":"{\"path\":\"native.go\"}"}}]},"finish_reason":"tool_calls"}]}`
	chunks := completeWithFakeOpenAI(t, false, body)

	calls := collectToolCalls(chunks)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(calls))
	}
	if calls[0].ID != "native" || calls[0].Function.Name != "read" || calls[0].Function.Arguments != `{"path":"native.go"}` {
		t.Fatalf("native call not preserved: %#v", calls[0])
	}
	if content := collectContent(chunks); !strings.Contains(content, "wrong") {
		t.Fatalf("native calls should not trigger artifact stripping; content=%q", content)
	}
}

func TestProvider_LeavesPlainProseNonStreaming(t *testing.T) {
	content := "I will use tool_calls if the provider asks for them, but this is normal prose."
	chunks := completeWithFakeOpenAI(t, false, chatCompletionResponse(content, ""))

	if calls := collectToolCalls(chunks); len(calls) != 0 {
		t.Fatalf("tool calls = %d, want 0", len(calls))
	}
	if got := collectContent(chunks); got != content {
		t.Fatalf("content = %q, want %q", got, content)
	}
}

func TestProvider_RecoversLeakedToolCallsStreaming(t *testing.T) {
	artifact := `<tool_calls>[{"id":"c1","name":"read","arguments":{"path":"stream.go"}}]</tool_calls>`
	chunks := completeWithFakeOpenAI(t, true, sseChunks(
		chatChunkContent(artifact, "stop"),
		`[DONE]`,
	))

	calls := collectToolCalls(chunks)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1; chunks=%#v", len(calls), chunks)
	}
	if calls[0].Function.Name != "read" || calls[0].Function.Arguments != `{"path":"stream.go"}` {
		t.Fatalf("recovered call = %#v", calls[0])
	}
	if content := collectContent(chunks); content != "" {
		t.Fatalf("visible content = %q, want stripped artifact", content)
	}
}

func TestProvider_LeavesPlainProseStreaming(t *testing.T) {
	chunks := completeWithFakeOpenAI(t, true, sseChunks(
		chatChunkContent("Hello ", ""),
		chatChunkContent("world", "stop"),
		`[DONE]`,
	))

	if calls := collectToolCalls(chunks); len(calls) != 0 {
		t.Fatalf("tool calls = %d, want 0", len(calls))
	}
	if got := collectContent(chunks); got != "Hello world" {
		t.Fatalf("content = %q, want Hello world", got)
	}
}

func completeWithFakeOpenAI(t *testing.T, stream bool, response string) []llm.CompletionChunk {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		_, _ = w.Write([]byte(response))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	provider, err := openai.NewProvider("test-key", openai.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	seq, err := provider.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "read foo"}},
		Stream:   stream,
		Tools: []llm.Tool{{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "read",
				Description: "read a file",
			},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var out []llm.CompletionChunk
	for chunk, err := range seq {
		if err != nil {
			t.Fatalf("completion chunk error: %v", err)
		}
		out = append(out, chunk)
	}
	return out
}

func chatCompletionResponse(content, finishReason string) string {
	if finishReason == "" {
		finishReason = "stop"
	}
	return fmt.Sprintf(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":%q}]}`, content, finishReason)
}

func chatChunkContent(content, finishReason string) string {
	finish := "null"
	if finishReason != "" {
		finish = fmt.Sprintf("%q", finishReason)
	}
	return fmt.Sprintf(`{"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":%q},"finish_reason":%s}]}`, content, finish)
}

func sseChunks(events ...string) string {
	var b strings.Builder
	for _, event := range events {
		b.WriteString("data: ")
		b.WriteString(event)
		b.WriteString("\n\n")
	}
	return b.String()
}

func collectToolCalls(chunks []llm.CompletionChunk) []llm.ToolCall {
	var calls []llm.ToolCall
	for _, chunk := range chunks {
		calls = append(calls, chunk.ToolCalls...)
	}
	return calls
}

func collectContent(chunks []llm.CompletionChunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString(chunk.Content)
	}
	return b.String()
}
