package openai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestProviderSerializesReasoningHistory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mode          llm.ReasoningHistory
		content       string
		reasoning     string
		wantContent   string
		wantReasoning string
	}{
		{name: "inline", mode: llm.ReasoningHistories.INLINE, content: "visible", reasoning: "hidden", wantContent: "<think>hidden</think>visible"},
		{name: "inline without reasoning", mode: llm.ReasoningHistories.INLINE, content: "visible", wantContent: "visible"},
		{name: "field", mode: llm.ReasoningHistories.FIELD, content: "visible", reasoning: "hidden chain", wantContent: "visible", wantReasoning: "hidden chain"},
		{name: "strip", mode: llm.ReasoningHistories.STRIP, content: "visible", reasoning: "hidden chain", wantContent: "visible"},
		{name: "field pure thinking", mode: llm.ReasoningHistories.FIELD, reasoning: "only thinking", wantReasoning: "only thinking"},
		{name: "strip pure thinking", mode: llm.ReasoningHistories.STRIP, reasoning: "only thinking"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\ndata: [DONE]\n\n"))
			}))
			defer server.Close()

			p, err := openai.NewProvider("test-key", openai.WithBaseURL(server.URL), openai.WithReasoningHistory(tc.mode))
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleAssistant, Content: tc.content, ReasoningContent: tc.reasoning}}}) {
				if err != nil {
					t.Fatalf("Complete: %v", err)
				}
			}
			messages := body["messages"].([]any)
			message := messages[0].(map[string]any)
			if message["content"] != tc.wantContent {
				t.Fatalf("content = %#v, want %q; body=%v", message["content"], tc.wantContent, body)
			}
			if tc.wantReasoning == "" {
				if _, ok := message["reasoning_content"]; ok {
					t.Fatalf("reasoning_content present, want absent; body=%v", body)
				}
			} else if message["reasoning_content"] != tc.wantReasoning {
				t.Fatalf("reasoning_content = %#v, want %q", message["reasoning_content"], tc.wantReasoning)
			}
		})
	}
}

func TestProviderReasoningKeepMask(t *testing.T) {
	t.Parallel()
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	mask := func(messages []llm.Message) []bool {
		if len(messages) != 4 {
			t.Errorf("mask received %d messages, want 4", len(messages))
		}
		return []bool{false, true, false, false}
	}
	p, err := openai.NewProvider("test-key", openai.WithBaseURL(server.URL), openai.WithReasoningHistory(llm.ReasoningHistories.FIELD), openai.WithReasoningKeepMask(mask))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	messages := []llm.Message{{Role: llm.RoleUser, Content: "q1"}, {Role: llm.RoleAssistant, Content: "a1", ReasoningContent: "keep"}, {Role: llm.RoleUser, Content: "q2"}, {Role: llm.RoleAssistant, Content: "a2", ReasoningContent: "drop"}}
	for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: messages}) {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	wire := body["messages"].([]any)
	if got := wire[1].(map[string]any)["reasoning_content"]; got != "keep" {
		t.Fatalf("kept reasoning = %#v, want keep", got)
	}
	if _, ok := wire[3].(map[string]any)["reasoning_content"]; ok {
		t.Fatalf("dropped reasoning_content present; body=%v", body)
	}
}

func TestProviderExtractsCompatibleReasoningFields(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"reasoning_content", "reasoning", "reasoning_summary", "thinking", "thought"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"" + field + "\":\"thinking...\"},\"index\":0}]}\n\ndata: [DONE]\n\n"))
			}))
			defer server.Close()
			p, err := openai.NewProvider("test-key", openai.WithBaseURL(server.URL))
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			var thinking strings.Builder
			for chunk, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true}) {
				if err != nil {
					t.Fatalf("Complete: %v", err)
				}
				thinking.WriteString(chunk.Thinking)
			}
			if thinking.String() != "thinking..." {
				t.Fatalf("thinking = %q, want thinking...", thinking.String())
			}
		})
	}
}
