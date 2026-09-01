package deepseek_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
)

func TestProviderDowngradesJSONSchemaWithoutMutatingRequest(t *testing.T) {
	t.Parallel()

	var captured []byte
	server := newCaptureServer(t, &captured)
	provider, err := deepseek.NewProvider("test-key", deepseek.WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"agent": map[string]any{"type": "string"}},
	}
	request := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "route the task"},
			{Role: llm.RoleUser, Content: "Task: fix the bug"},
		},
		Stream:         true,
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.SchemaFromMap(schema)},
	}

	consume(t, provider.Complete(t.Context(), request))
	if request.Messages[1].Content != "Task: fix the bug" {
		t.Fatalf("input message mutated: %q", request.Messages[1].Content)
	}
	if request.ResponseFormat.Type != llm.ResponseFormatJSONSchema {
		t.Fatalf("input response format mutated: %q", request.ResponseFormat.Type)
	}

	body := decodeBody(t, captured)
	responseFormat := body["response_format"].(map[string]any)
	if got := responseFormat["type"]; got != string(llm.ResponseFormatJSONObject) {
		t.Fatalf("response_format.type = %q, want json_object", got)
	}
	messages := body["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	content := last["content"].(string)
	if !strings.HasPrefix(content, "Task: fix the bug") {
		t.Errorf("directive replaced user content: %q", content)
	}
	if !strings.Contains(strings.ToLower(content), "json") || !strings.Contains(content, `"agent"`) {
		t.Errorf("user message lacks JSON schema directive: %q", content)
	}
}

func TestProviderAddsJSONObjectKeywordOnlyWhenNeeded(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		content     string
		wantChanged bool
	}{
		{name: "keyword present", content: "reply as JSON please"},
		{name: "keyword absent", content: "reply with the answer", wantChanged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var captured []byte
			server := newCaptureServer(t, &captured)
			provider, err := deepseek.NewProvider("test-key", deepseek.WithBaseURL(server.URL))
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			consume(t, provider.Complete(t.Context(), llm.CompletionRequest{
				Messages:       []llm.Message{{Role: llm.RoleUser, Content: tc.content}},
				Stream:         true,
				ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject},
			}))

			messages := decodeBody(t, captured)["messages"].([]any)
			got := messages[0].(map[string]any)["content"].(string)
			if changed := got != tc.content; changed != tc.wantChanged {
				t.Fatalf("content = %q, changed = %v, want changed %v", got, changed, tc.wantChanged)
			}
			if !strings.Contains(strings.ToLower(got), "json") {
				t.Fatalf("content lacks JSON keyword: %q", got)
			}
		})
	}
}

func newCaptureServer(t *testing.T, captured *[]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		*captured, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"ok"},"finish_reason":"stop","index":0}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)
	return server
}

func consume(t *testing.T, stream llm.CompletionStream) {
	t.Helper()
	for _, err := range stream {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
}

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode request: %v\nbody: %s", err, raw)
	}
	return body
}
