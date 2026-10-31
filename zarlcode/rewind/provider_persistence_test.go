package rewind_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/db"
)

// reopenProviderHistory crosses the actual durable session boundary, not an
// in-memory codec round trip. Historical arrays must stay ordinary arrays.
func reopenProviderHistory(t *testing.T, route string, messages []llm.Message) []llm.Message {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	initialStore := store
	t.Cleanup(func() { _ = initialStore.Close() })
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(t.Context(), db.SessionRecord{ID: "resume", Workspace: "/workspace", Provider: route, Model: "fixture", ContextJSON: raw}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record, err := store.GetSession(t.Context(), "resume")
	if err != nil {
		t.Fatal(err)
	}
	var restored []llm.Message
	if err := json.Unmarshal(record.ContextJSON, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(messages, restored) {
		t.Fatal("stored history changed")
	}
	if err := rewind.ValidateLegacyContext(restored, record.Provider); err != nil {
		t.Fatalf("reopened %s history rejected: %v", route, err)
	}
	return restored
}

func completeHistory(t *testing.T, provider llm.Provider, request llm.CompletionRequest) llm.Message {
	t.Helper()
	request.Stream = true
	message := llm.Message{Role: llm.RoleAssistant}
	for chunk, err := range provider.Complete(t.Context(), request) {
		if err != nil {
			t.Fatal(err)
		}
		chunk = chunk.Clone()
		message.Content += chunk.Content
		message.ReasoningContent += chunk.Thinking
		message.ToolCalls = append(message.ToolCalls, chunk.ToolCalls...)
		message.ContinuationItems = append(message.ContinuationItems, chunk.CompletedItems...)
		if chunk.ContentOutputIndex != nil {
			message.ContentOutputIndex = chunk.ContentOutputIndex
		}
	}
	return message
}

func TestStoredGoogleHistoryReplaysSignedCallsAndInlineImage(t *testing.T) {
	for _, route := range []string{"gemini", "google-vertex"} {
		t.Run(route, func(t *testing.T) {
			var requests []json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requests = append(requests, body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"private reasoning","thought":true},{"text":"calling tools"},{"functionCall":{"name":"read","args":{"path":"one"}},"thoughtSignature":"c2lnbmVk"},{"functionCall":{"id":"second","name":"read","args":{"path":"two"}},"thoughtSignature":"c2Vjb25k"}]},"finishReason":"STOP"}]}`+"\n\n")
			}))
			t.Cleanup(server.Close)
			var p *google.Provider
			var err error
			if route == "gemini" {
				p, err = google.NewProvider("fixture-key", google.WithBaseURL(server.URL), google.WithModel("fixture"))
			} else {
				client, clientErr := genai.NewClient(t.Context(), &genai.ClientConfig{Backend: genai.BackendVertexAI, Project: "fixture-project", Location: "global", HTTPClient: server.Client(), HTTPOptions: genai.HTTPOptions{BaseURL: server.URL}})
				if clientErr != nil {
					t.Fatal(clientErr)
				}
				p, err = google.NewVertexProvider(t.Context(), "fixture-project", "global", google.WithClient(client), google.WithModel("fixture"))
			}
			if err != nil {
				t.Fatal(err)
			}
			user := llm.Message{Role: llm.RoleUser, Content: "one prompt", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "one prompt"}, {Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png"}}}}
			assistant := completeHistory(t, p, llm.CompletionRequest{Messages: []llm.Message{user}})
			if len(assistant.ToolCalls) != 2 || len(assistant.ContinuationItems) != 2 || assistant.ReasoningContent != "private reasoning" || assistant.Content != "calling tools" {
				t.Fatalf("capture missing fields: %#v", assistant)
			}
			history := []llm.Message{user, assistant}
			for _, call := range assistant.ToolCalls {
				history = append(history, llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: "ok"})
			}
			completeHistory(t, p, llm.CompletionRequest{Messages: history})
			restored := reopenProviderHistory(t, route, history)
			completeHistory(t, p, llm.CompletionRequest{Messages: restored})
			var live, resumed any
			if err := json.Unmarshal(requests[1], &live); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(requests[2], &resumed); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(live, resumed) {
				t.Fatal("reopened request differs from uninterrupted request")
			}
			if strings.Count(string(requests[2]), "one prompt") != 1 {
				t.Fatal("duplicated user prompt")
			}
			if strings.Contains(string(requests[2]), "private reasoning") {
				t.Fatal("display reasoning sent outbound")
			}
			var wire struct {
				Contents []struct {
					Parts []struct {
						Text       string `json:"text"`
						InlineData *struct {
							Data     string `json:"data"`
							MIMEType string `json:"mimeType"`
						} `json:"inlineData"`
						ThoughtSignature string `json:"thoughtSignature"`
					} `json:"parts"`
				} `json:"contents"`
			}
			if err := json.Unmarshal(requests[2], &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Contents) != 3 || len(wire.Contents[0].Parts) != 2 || wire.Contents[0].Parts[1].InlineData == nil || wire.Contents[0].Parts[1].InlineData.Data != "YQ==" || wire.Contents[0].Parts[1].InlineData.MIMEType != "image/png" {
				t.Fatal("inline image lost")
			}
			if len(wire.Contents[1].Parts) != 3 || wire.Contents[1].Parts[0].Text != "calling tools" || wire.Contents[1].Parts[1].ThoughtSignature != "c2lnbmVk" || wire.Contents[1].Parts[2].ThoughtSignature != "c2Vjb25k" {
				t.Fatal("signed call pairing lost")
			}
		})
	}
}

func TestStoredLocalAndDeepSeekReasoningMatchesLiveWire(t *testing.T) {
	for _, tc := range []struct {
		route, model string
		tools        bool
		reasoning    string
	}{
		{"llamacpp", "fixture", false, "inline"}, {"ollama", "fixture", false, "inline"},
		{"deepseek", "deepseek-reasoner", true, "strip"},
		{"deepseek", "deepseek-chat", true, "field"},
		{"deepseek", "deepseek-chat", false, "trim"},
		{"deepseek", "deepseek-chat", false, "tool window"},
	} {
		t.Run(tc.route+"/"+tc.reasoning, func(t *testing.T) {
			var requests [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requests = append(requests, body)
				w.Header().Set("Content-Type", "text/event-stream")
				response := `{"choices":[{"index":0,"delta":{"reasoning_content":"private reasoning","content":"answer"},"finish_reason":"stop"}]}`
				if tc.reasoning == "tool window" && len(requests) == 1 {
					response = `{"choices":[{"index":0,"delta":{"reasoning_content":"private reasoning","tool_calls":[{"index":0,"id":"read-one","type":"function","function":{"name":"read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
				}
				_, _ = io.WriteString(w, "data: "+response+"\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			var p llm.Provider
			if tc.route == "deepseek" {
				p = deepseek.NewProvider("fixture-key", deepseek.WithBaseURL(server.URL), deepseek.WithModel(tc.model))
			} else {
				var err error
				p, err = backends.NewRegistry().BuildWithConfig(t.Context(), tc.route, backends.BuildConfig{Model: tc.model, BaseURL: server.URL, APIKey: "fixture-key"})
				if err != nil {
					t.Fatal(err)
				}
			}
			user := llm.Message{Role: llm.RoleUser, Content: "first"}
			if tc.route == "llamacpp" || tc.route == "ollama" {
				user.Parts = []llm.ContentPart{{Type: llm.ContentTypeText, Text: "first"}, {Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png"}}}
			}
			assistant := completeHistory(t, p, llm.CompletionRequest{Messages: []llm.Message{user}})
			if assistant.ReasoningContent != "private reasoning" {
				t.Fatal("reasoning not captured")
			}
			history := []llm.Message{user, assistant}
			if tc.reasoning == "tool window" {
				if len(assistant.ToolCalls) != 1 {
					t.Fatal("tool call not captured")
				}
				history = append(history, llm.Message{Role: llm.RoleTool, ToolCallID: assistant.ToolCalls[0].ID, Content: "read ok"})
			}
			history = append(history, llm.Message{Role: llm.RoleUser, Content: "next"})
			request := llm.CompletionRequest{Messages: history}
			if tc.tools {
				request.Tools = []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "read", Parameters: llm.Schema{Type: "object"}}}}
			}
			completeHistory(t, p, request)
			request.Messages = reopenProviderHistory(t, tc.route, history)
			completeHistory(t, p, request)
			var live, resumed any
			if err := json.Unmarshal(requests[1], &live); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(requests[2], &resumed); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(live, resumed) {
				t.Fatal("reopened request differs from uninterrupted request")
			}
			wire := string(requests[2])
			if (tc.route == "llamacpp" || tc.route == "ollama") && (!strings.Contains(wire, "data:image/png;base64,YQ==") || strings.Count(wire, "first") != 1) {
				t.Fatal("local inline image lost or prompt duplicated")
			}
			switch tc.reasoning {
			case "inline":
				if !strings.Contains(wire, "<think>private reasoning</think>") {
					t.Fatal("reasoning not rewrapped")
				}
			case "field", "tool window":
				if !strings.Contains(wire, `"reasoning_content":"private reasoning"`) {
					t.Fatal("reasoning field not replayed")
				}
			case "strip", "trim":
				if strings.Contains(wire, "private reasoning") {
					t.Fatal("reasoning unexpectedly replayed")
				}
			}
		})
	}
}
