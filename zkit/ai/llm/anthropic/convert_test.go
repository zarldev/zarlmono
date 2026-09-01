package anthropic_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	anthropicprovider "github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestProviderSerializesAgenticConversation(t *testing.T) {
	body := anthropicRequestBody(t, llm.CompletionRequest{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "you are a tool"},
		{Role: llm.RoleUser, Content: "read x"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "t1", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"x"}`}}}},
		{Role: llm.RoleTool, ToolCallID: "t1", Content: "file contents"},
		{Role: llm.RoleAssistant, Content: "done"},
	}})

	messages := body["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4 (system hoisted)", len(messages))
	}
	if got := messages[0].(map[string]any)["role"]; got != "user" {
		t.Fatalf("messages[0].role = %v, want user", got)
	}
	toolUse := messageContent(t, messages[1])[0]
	if toolUse["type"] != "tool_use" || toolUse["id"] != "t1" || toolUse["name"] != "read" {
		t.Errorf("tool_use = %#v", toolUse)
	}
	if got := toolUse["input"].(map[string]any)["path"]; got != "x" {
		t.Errorf("tool input path = %v, want x", got)
	}
	toolResult := messageContent(t, messages[2])[0]
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "t1" {
		t.Errorf("tool_result = %#v", toolResult)
	}
	if got := messageContent(t, messages[3])[0]["text"]; got != "done" {
		t.Errorf("trailing text = %v, want done", got)
	}
}

func TestProviderCoalescesToolResultsAndDefaultsEmptyArguments(t *testing.T) {
	body := anthropicRequestBody(t, llm.CompletionRequest{Messages: []llm.Message{
		{Role: llm.RoleAssistant},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "a", Function: llm.ToolCallFunction{Name: "read", Arguments: "  "}},
			{ID: "b", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"p":2}`}},
		}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: "one"},
		{Role: llm.RoleTool, ToolCallID: "b", Content: "two"},
	}})

	messages := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want assistant plus coalesced user", len(messages))
	}
	uses := messageContent(t, messages[0])
	if len(uses) != 2 {
		t.Fatalf("tool uses = %d, want 2", len(uses))
	}
	if got := uses[0]["input"].(map[string]any); len(got) != 0 {
		t.Errorf("blank arguments input = %#v, want empty object", got)
	}
	results := messageContent(t, messages[1])
	if len(results) != 2 || results[0]["type"] != "tool_result" || results[1]["type"] != "tool_result" {
		t.Errorf("coalesced results = %#v", results)
	}
}

func TestProviderSetsStaticAndRollingCacheBreakpoints(t *testing.T) {
	body := anthropicRequestBody(t, llm.CompletionRequest{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "read x"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "t1", Function: llm.ToolCallFunction{Name: "read", Arguments: `{}`}}}},
		{Role: llm.RoleTool, ToolCallID: "t1", Content: "contents"},
	}})

	system := body["system"].([]any)[0].(map[string]any)
	static := system["cache_control"].(map[string]any)
	if static["type"] != "ephemeral" || static["ttl"] != "1h" {
		t.Errorf("static cache control = %#v", static)
	}
	messages := body["messages"].([]any)
	first := messageContent(t, messages[0])[0]
	if _, ok := first["cache_control"]; ok {
		t.Errorf("first block unexpectedly marked: %#v", first)
	}
	tail := messageContent(t, messages[len(messages)-1])[0]
	rolling := tail["cache_control"].(map[string]any)
	if rolling["type"] != "ephemeral" {
		t.Errorf("rolling cache control = %#v", rolling)
	}
	if _, ok := rolling["ttl"]; ok {
		t.Errorf("rolling cache control should use default TTL: %#v", rolling)
	}
}

func anthropicRequestBody(t *testing.T, req llm.CompletionRequest) map[string]any {
	t.Helper()
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0}}`))
	}))
	t.Cleanup(server.Close)

	provider, err := anthropicprovider.NewProvider("test-key", anthropicprovider.WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	for _, completeErr := range provider.Complete(t.Context(), req) {
		if completeErr != nil {
			t.Fatalf("Complete: %v", completeErr)
		}
	}
	return <-requests
}

func messageContent(t *testing.T, message any) []map[string]any {
	t.Helper()
	raw := message.(map[string]any)["content"].([]any)
	content := make([]map[string]any, len(raw))
	for i := range raw {
		content[i] = raw[i].(map[string]any)
	}
	return content
}
