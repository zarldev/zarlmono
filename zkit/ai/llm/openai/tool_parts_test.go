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

func TestProviderSerializesChatToolResultAttachments(t *testing.T) {
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
	provider := openai.NewProvider("test-key", openai.WithBaseURL(server.URL))

	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Function: llm.ToolCallFunction{Name: "computer_observe", Arguments: `{}`}}}},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: "metadata", Parts: []llm.ContentPart{llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/png")}},
	}
	for _, streamErr := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: messages}) {
		if streamErr != nil {
			t.Fatalf("Complete: %v", streamErr)
		}
	}

	wire := body["messages"].([]any)
	if len(wire) != 3 || wire[1].(map[string]any)["role"] != "tool" || wire[2].(map[string]any)["role"] != "user" {
		t.Fatalf("messages = %#v", wire)
	}
	parts := wire[2].(map[string]any)["content"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["type"] != "text" || parts[1].(map[string]any)["type"] != "image_url" {
		t.Fatalf("attachment parts = %#v", parts)
	}
	if !strings.Contains(parts[0].(map[string]any)["text"].(string), "call_1") {
		t.Fatalf("attachment attribution = %#v", parts[0])
	}
	image := parts[1].(map[string]any)["image_url"].(map[string]any)
	if image["url"] != "data:image/png;base64,cG5n" {
		t.Fatalf("image = %#v", image)
	}
}
