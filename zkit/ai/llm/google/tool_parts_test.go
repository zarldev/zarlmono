package google_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestProviderSerializesToolResultAttachments(t *testing.T) {
	t.Parallel()
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"))
	}))
	defer server.Close()
	provider, err := google.NewProvider("test-key", google.WithBaseURL(server.URL), google.WithModel("gemini-2.0-flash"))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "screen-1", Function: llm.ToolCallFunction{Name: "computer_observe", Arguments: `{}`}}}},
		{Role: llm.RoleTool, ToolCallID: "screen-1", Content: "metadata", Parts: []llm.ContentPart{llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/png")}},
	}
	for _, streamErr := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages}) {
		if streamErr != nil {
			t.Fatalf("Complete: %v", streamErr)
		}
	}

	contents := request["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents = %#v", contents)
	}
	parts := contents[2].(map[string]any)["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("attachment parts = %#v", parts)
	}
	if !strings.Contains(parts[0].(map[string]any)["text"].(string), "screen-1") {
		t.Fatalf("attachment attribution = %#v", parts[0])
	}
	inline := parts[1].(map[string]any)["inlineData"].(map[string]any)
	if inline["mimeType"] != "image/png" || inline["data"] != "cG5n" {
		t.Fatalf("inline image = %#v", inline)
	}
}
