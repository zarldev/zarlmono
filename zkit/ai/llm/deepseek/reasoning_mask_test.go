package deepseek_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
)

func TestProviderKeepsReasoningOnlyInToolCallWindows(t *testing.T) {
	t.Parallel()

	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		captured, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"ok"},"finish_reason":"stop","index":0}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	provider, err := deepseek.NewProvider("test-key",
		deepseek.WithBaseURL(server.URL),
		deepseek.WithModel("deepseek-v4"),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "use a tool"},
		{Role: llm.RoleAssistant, ReasoningContent: "keep-call", ToolCalls: []llm.ToolCall{{ID: "call-1", Type: "function", Function: llm.ToolCallFunction{Name: "lookup", Arguments: `{}`}}}},
		{Role: llm.RoleTool, Content: "result", ToolCallID: "call-1"},
		{Role: llm.RoleAssistant, Content: "tool answer", ReasoningContent: "keep-answer"},
		{Role: llm.RoleUser, Content: "no tool now"},
		{Role: llm.RoleAssistant, Content: "plain answer", ReasoningContent: "drop-answer"},
	}
	consume(t, provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages, Stream: true}))

	body := decodeBody(t, captured)
	wireMessages := body["messages"].([]any)
	assertReasoning(t, wireMessages[1], "keep-call")
	assertReasoning(t, wireMessages[3], "keep-answer")
	assertReasoning(t, wireMessages[5], "")
}

func assertReasoning(t *testing.T, raw any, want string) {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(encoded, &message); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	got, present := message["reasoning_content"]
	if want == "" {
		if present {
			t.Fatalf("reasoning_content = %v, want absent", got)
		}
		return
	}
	if got != want {
		t.Fatalf("reasoning_content = %v, want %q", got, want)
	}
}
