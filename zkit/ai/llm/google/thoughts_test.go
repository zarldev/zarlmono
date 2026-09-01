package google_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestProvider_ThoughtAndAnswerChannelsStayDisjoint(t *testing.T) {
	server := googleStreamServer(t, `{"candidates":[{"content":{"role":"model","parts":[{"text":"step 1, ","thought":true},{"text":"step 2","thought":true},{"text":"the answer is 42"}]},"finishReason":"STOP"}]}`)
	defer server.Close()

	chunks := completeGoogle(t, server.URL, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "answer"}},
	})
	var content, thinking strings.Builder
	for _, chunk := range chunks {
		content.WriteString(chunk.Content)
		thinking.WriteString(chunk.Thinking)
	}
	if got := thinking.String(); got != "step 1, step 2" {
		t.Errorf("thinking = %q, want %q", got, "step 1, step 2")
	}
	if got := content.String(); got != "the answer is 42" {
		t.Errorf("content = %q, want %q", got, "the answer is 42")
	}
}

func TestProvider_ThoughtsAndToolCallsAreBothSurfaced(t *testing.T) {
	server := googleStreamServer(t, `{"candidates":[{"content":{"role":"model","parts":[{"text":"i need to read foo.go","thought":true},{"functionCall":{"id":"call_1","name":"read","args":{"path":"foo.go"}}}]},"finishReason":"STOP"}]}`)
	defer server.Close()

	chunks := completeGoogle(t, server.URL, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "read it"}},
	})
	var thinking strings.Builder
	var calls []llm.ToolCall
	for _, chunk := range chunks {
		thinking.WriteString(chunk.Thinking)
		calls = append(calls, chunk.ToolCalls...)
	}
	if got := thinking.String(); got != "i need to read foo.go" {
		t.Errorf("thinking = %q, want %q", got, "i need to read foo.go")
	}
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(calls))
	}
	if calls[0].ID != "call_1" || calls[0].Function.Name != "read" {
		t.Errorf("tool call = %#v, want ID call_1 and name read", calls[0])
	}
}

func TestProvider_RequestsThoughts(t *testing.T) {
	var body strings.Builder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body.Write(data)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"))
	}))
	defer server.Close()

	completeGoogle(t, server.URL, llm.CompletionRequest{
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "think"}},
		Temperature: 0.4,
	})
	if !strings.Contains(body.String(), `"thinkingConfig":{"includeThoughts":true}`) {
		t.Errorf("request did not enable thoughts: %s", body.String())
	}
}

func googleStreamServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + response + "\n\n"))
	}))
}

func completeGoogle(t *testing.T, baseURL string, req llm.CompletionRequest) []llm.CompletionChunk {
	t.Helper()
	p, err := google.NewProvider("test-key", google.WithBaseURL(baseURL), google.WithModel("gemini-2.0-flash"))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	var chunks []llm.CompletionChunk
	for chunk, err := range p.Complete(t.Context(), req) {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		chunks = append(chunks, chunk.Clone())
	}
	return chunks
}
