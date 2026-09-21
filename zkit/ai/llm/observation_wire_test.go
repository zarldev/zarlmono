package llm_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestHostObservationUsesSupportedUserWireShape(t *testing.T) {
	for _, route := range []struct {
		name     string
		path     string
		new      func(*testing.T, string) llm.Provider
		response string
		stream   bool
	}{
		{name: "openai-chat", path: "/chat/completions", new: func(_ *testing.T, url string) llm.Provider {
			return openai.NewProvider("test-key", openai.WithBaseURL(url))
		}, response: `{"id":"chat","object":"chat.completion","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`},
		{name: "llamacpp", path: "/chat/completions", new: func(_ *testing.T, url string) llm.Provider {
			return llamacpp.NewProvider(llamacpp.WithBaseURL(url))
		}, response: `{"id":"chat","object":"chat.completion","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`},
		{name: "anthropic", path: "/messages", new: func(_ *testing.T, url string) llm.Provider {
			return anthropic.NewProvider("test-key", anthropic.WithBaseURL(url))
		}, response: `{"id":"msg","type":"message","role":"assistant","model":"fixture","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`},
		{name: "openai-responses", path: "/responses", new: func(_ *testing.T, url string) llm.Provider {
			return openai.NewProvider("test-key", openai.WithBaseURL(url), openai.WithResponsesAPI(true), openai.WithModel("gpt-5.6"))
		}, response: observationResponseSSE, stream: true},
		{name: "codex", path: "/codex/responses", new: func(_ *testing.T, url string) llm.Provider {
			return openaicodex.NewProvider(openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "fixture-token", AccountID: "fixture-account", Expires: time.Now().Add(time.Hour)}}, openaicodex.WithBaseURL(url))
		}, response: observationResponseSSE, stream: true},
		{name: "gemini", path: ":streamGenerateContent", new: func(t *testing.T, url string) llm.Provider {
			provider, err := google.NewProvider("test-key", google.WithBaseURL(url), google.WithModel("gemini-2.0-flash"))
			if err != nil {
				t.Fatal(err)
			}
			return provider
		}, response: "data: " + `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1}}` + "\n\n", stream: true},
	} {
		t.Run(route.name, func(t *testing.T) {
			captured := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, route.path) {
					t.Errorf("route = %q, want suffix %q", r.URL.Path, route.path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				select {
				case captured <- body:
				default:
					t.Error("unexpected repeated fixture request")
				}
				w.Header().Set("Content-Type", "application/json")
				if route.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				_, _ = io.WriteString(w, route.response)
			}))
			defer server.Close()
			provider := route.new(t, server.URL)
			request := llm.CompletionRequest{Messages: []llm.Message{
				{Role: llm.RoleUser, Content: "original assignment"},
				{Role: llm.RoleAssistant, Content: "provisional answer"},
				{Role: llm.RoleUser, Content: "host-observation-marker", Observation: llm.ObservationProvenance{Version: 1, ID: "private-origin-id"}},
			}, Stream: route.stream}
			for _, err := range provider.Complete(t.Context(), request) {
				if err != nil {
					t.Fatal(err)
				}
			}
			select {
			case body := <-captured:
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encoded), "private-origin-id") || strings.Contains(string(encoded), `"observation"`) {
					t.Fatal("host-only provenance leaked into provider schema")
				}
				if !userWireContainsObservation(body) {
					t.Fatalf("host input was not carried by a supported user message: %s", encoded)
				}
			default:
				t.Fatal("provider did not send the fixture request")
			}
			if request.Messages[2].Observation.ID != "private-origin-id" {
				t.Fatal("provider shaping mutated canonical provenance")
			}
		})
	}
}

func userWireContainsObservation(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		if value["role"] == "user" {
			encoded, _ := json.Marshal(value)
			if strings.Contains(string(encoded), "host-observation-marker") {
				return true
			}
		}
		for _, child := range value {
			if userWireContainsObservation(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if userWireContainsObservation(child) {
				return true
			}
		}
	}
	return false
}

const observationResponseSSE = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg\",\"output_index\":0,\"content_index\":0,\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp\",\"status\":\"completed\",\"model\":\"fixture\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n"
