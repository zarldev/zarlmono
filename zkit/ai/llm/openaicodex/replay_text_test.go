package openaicodex_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestProviderReplaysNativeTextAroundToolCall(t *testing.T) {
	t.Parallel()
	const before = `{"type":"message","id":"msg_before","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"before"}]}`
	const after = `{"type":"message","id":"msg_after","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"after"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Input) != 4 {
			t.Errorf("input count=%d, want 4", len(request.Input))
		} else {
			if string(request.Input[0]) != before || string(request.Input[2]) != after {
				t.Error("native text bytes or order changed")
			}
			// Check tool position independently of native-text suppression.
			var call struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal(request.Input[1], &call); err != nil {
				t.Error(err)
			}
			if call.Type != "function_call" || call.CallID != "call" || call.Name != "read" || call.Arguments != `{}` {
				t.Error("tool call changed or moved")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}))
	t.Cleanup(server.Close)
	provider := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: freshToken(t, "acct")}, openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry())
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: "beforeafter", ContentOutputIndex: llm.OutputPosition(2), ContinuationItems: []llm.ContinuationItem{
			{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "message", ID: "msg_after", OutputIndex: llm.OutputPosition(8), Data: []byte(after)},
			{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "message", ID: "msg_before", OutputIndex: llm.OutputPosition(2), Data: []byte(before)},
		}, ToolCalls: []llm.ToolCall{{ID: "call", Type: "function", OutputIndex: llm.OutputPosition(5), Function: llm.ToolCallFunction{Name: "read", Arguments: `{}`}}}},
		{Role: llm.RoleTool, ToolCallID: "call", Content: "result"},
	}
	original := llm.CloneMessages(messages)
	for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(messages, original) {
		t.Fatal("replay mutated borrowed context")
	}
}
