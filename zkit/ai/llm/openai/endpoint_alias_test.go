package openai_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestEndpointAliasRequests(t *testing.T) {
	t.Parallel()

	for _, model := range []struct {
		name, id         string
		defaultResponses bool
	}{
		{name: "opaque alias", id: " Company/Production Alias-V7 "},
		{name: "unknown GPT alias", id: "gpt-6-unknown"},
		{name: "stock Responses model", id: "gpt-5.6", defaultResponses: true},
	} {
		for _, customURL := range []bool{false, true} {
			for _, policy := range []struct {
				name              string
				explicit, enabled bool
			}{
				{name: "default"},
				{name: "explicit Responses", explicit: true, enabled: true},
				{name: "explicit Chat", explicit: true},
			} {
				for _, reverse := range []bool{false, true} {
					for _, stream := range []bool{false, true} {
						for _, tools := range []bool{false, true} {
							name := fmt.Sprintf("%s/custom=%t/%s/reverse=%t/stream=%t/tools=%t", model.name, customURL, policy.name, reverse, stream, tools)
							t.Run(name, func(t *testing.T) {
								t.Parallel()
								responses := model.defaultResponses && !customURL
								if policy.explicit {
									responses = policy.enabled
								}
								runEndpointAliasRequest(t, model.id, customURL, policy.explicit, policy.enabled, reverse, stream, tools, responses)
							})
						}
					}
				}
			}
		}
	}
}

func runEndpointAliasRequest(t *testing.T, model string, customURL, explicit, enabled, reverse, stream, tools, responses bool) {
	t.Helper()

	type capturedRequest struct {
		path string
		body map[string]any
	}
	requests := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		requests <- capturedRequest{path: r.URL.Path, body: body}
		if !stream && r.URL.Path == "/responses" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"r1","status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}`))
			return
		}
		if !stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop","index":0}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Path == "/responses" {
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
			return
		}
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\",\"index\":0}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	opts := []options.Option[openai.Provider]{openai.WithModel(model)}
	if customURL {
		opts = append(opts, openai.WithBaseURL(server.URL))
	} else {
		// Exercise the stock URL policy without making any external requests.
		opts = append(opts, openai.WithBaseURL("https://api.openai.com/v1"), openai.WithHTTPClient(&http.Client{Transport: redirectTransport{target: server.URL}}))
	}
	if explicit {
		opts = append(opts, openai.WithResponsesAPI(enabled))
	}
	if reverse {
		slices.Reverse(opts)
	}
	provider := openai.NewProvider("test-key", opts...)
	req := llm.CompletionRequest{
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Stream:    stream,
		MaxTokens: 42,
	}
	if tools {
		req.Tools = []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "read", Parameters: llm.SchemaFromMap(map[string]any{"type": "object"})}}}
	}
	var content strings.Builder
	var terminalErr error
	for chunk, err := range provider.Complete(t.Context(), req) {
		if terminalErr != nil {
			t.Fatal("yield after terminal error")
		}
		terminalErr = err
		content.WriteString(chunk.Content)
	}
	if terminalErr != nil {
		t.Fatalf("Complete: %v", terminalErr)
	}
	if content.String() != "ok" {
		t.Fatalf("content = %q, want ok", content.String())
	}
	var got capturedRequest
	select {
	case got = <-requests:
	default:
		t.Fatal("no HTTP request")
	}
	wantPath, tokenField := "/chat/completions", "max_tokens"
	if model == "gpt-5.6" {
		tokenField = "max_completion_tokens"
	}
	if responses {
		wantPath, tokenField = "/responses", "max_output_tokens"
	}
	if got.path != wantPath {
		t.Fatalf("request path = %q, want %q", got.path, wantPath)
	}
	if got.body["model"] != model {
		t.Errorf("model = %#v, want exact alias %q", got.body["model"], model)
	}
	if got.body["stream"] != stream && (stream || got.body["stream"] != nil) {
		t.Errorf("stream = %#v, want %t (or omitted when false)", got.body["stream"], stream)
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if field == tokenField {
			if got.body[field] != float64(42) {
				t.Errorf("%s = %#v, want 42", field, got.body[field])
			}
		} else if _, present := got.body[field]; present {
			t.Errorf("unexpected %s in request", field)
		}
	}
	wireTools, _ := got.body["tools"].([]any)
	wantTools := 0
	if tools {
		wantTools = 1
	}
	if len(wireTools) != wantTools {
		t.Errorf("tools = %#v, want %d tools", got.body["tools"], wantTools)
	}
}
