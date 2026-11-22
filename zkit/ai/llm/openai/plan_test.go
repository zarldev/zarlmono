package openai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

func boolPtr(value bool) *bool { return &value }

func TestProviderPlansRequestsOnTheWire(t *testing.T) {
	t.Parallel()

	tool := llm.Tool{Type: "function", Function: llm.ToolFunction{Name: "read", Parameters: llm.SchemaFromMap(map[string]any{"type": "object"})}}
	type requestCase struct {
		name, model, path, tokenField string
		tools, thinking, stream       bool
		responsesAPI                  *bool
		publicURL                     bool
		reasoningEffort               string
	}
	tests := []requestCase{
		{name: "ordinary chat", model: "gpt-4o-mini", path: "/chat/completions", tokenField: "max_tokens", stream: true},
		{name: "ordinary chat with tools", model: "gpt-4o-mini", path: "/chat/completions", tokenField: "max_tokens", tools: true, stream: true},
		{name: "o3 without tools", model: "o3", path: "/chat/completions", tokenField: "max_completion_tokens", stream: true},
		{name: "gpt 5.6 without tools", model: "gpt-5.6", path: "/responses", stream: true},
		{name: "gpt 5.6 non-stream tools", model: "gpt-5.6", path: "/responses", tokenField: "max_output_tokens", tools: true, responsesAPI: boolPtr(true)},
		{name: "gpt 5.6 public URL explicit chat", model: "gpt-5.6", path: "/chat/completions", tokenField: "max_completion_tokens", tools: true, publicURL: true, responsesAPI: boolPtr(false)},
		{name: "gpt 5.6 custom URL implicit chat", model: "gpt-5.6", path: "/chat/completions", tokenField: "max_completion_tokens", tools: true},
		{name: "gpt 5.6 custom URL explicit chat", model: "gpt-5.6", path: "/chat/completions", tokenField: "max_completion_tokens", tools: true, responsesAPI: boolPtr(false)},
		{name: "gpt 6 astra non-stream tools", model: "gpt-6-astra", path: "/responses", tokenField: "max_output_tokens", tools: true, responsesAPI: boolPtr(true)},
		{name: "gpt 6 astra non-stream without tools", model: "gpt-6-astra", path: "/chat/completions", tokenField: "max_completion_tokens"},
		{name: "o3 mini tools remain chat", model: "o3-mini", path: "/chat/completions", tokenField: "max_completion_tokens", tools: true, stream: true},
		{name: "gpt 5 tools and thinking", model: "gpt-5.6-sol", path: "/responses", tools: true, thinking: true, stream: true},
		{name: "gpt 6 astra max reasoning", model: "gpt-6-astra", path: "/responses", tokenField: "max_output_tokens", tools: true, thinking: true, stream: true, reasoningEffort: "max"},
		{name: "case and space variation", model: " GPT-5.6-SOL ", path: "/responses", tools: true, stream: true},
		{name: "unknown model stays on chat", model: "company-model-v7", path: "/chat/completions", tokenField: "max_tokens", tools: true, stream: true},
		{name: "local model stays on chat", model: "llama-3.3-local", path: "/chat/completions", tokenField: "max_tokens", tools: true, stream: true},
		{name: "gpt 6 sol non-stream tools", model: "gpt-6-sol", path: "/responses", tokenField: "max_output_tokens", tools: true, responsesAPI: boolPtr(true)},
		{name: "gpt 6 luna non-stream tools", model: "gpt-6-luna", path: "/responses", tokenField: "max_output_tokens", tools: true, responsesAPI: boolPtr(true)},
		{name: "gpt 6 sol plain stream", model: "gpt-6-sol", path: "/responses", stream: true},
		{name: "gpt 6 luna plain stream", model: "gpt-6-luna", path: "/responses", stream: true},
		{name: "unknown gpt 6 stays on chat", model: "gpt-6-unknown", path: "/chat/completions", tokenField: "max_tokens", tools: true, stream: true},
		{name: "gpt 6.1 sol non-stream tools", model: "gpt-6.1-sol", path: "/responses", tokenField: "max_output_tokens", tools: true, responsesAPI: boolPtr(true)},
		{name: "gpt 6.1 sol non-stream without tools", model: "gpt-6.1-sol", path: "/chat/completions", tokenField: "max_completion_tokens"},
		{name: "gpt 6.1 sol plain stream", model: "gpt-6.1-sol", path: "/responses", stream: true},
		{name: "unknown gpt 6.1 stays on chat", model: "gpt-6.1-unknown", path: "/chat/completions", tokenField: "max_tokens", tools: true, stream: true},
	}
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, effort := range []string{"", "none", "low", "medium", "high", "xhigh", "max"} {
			tests = append(tests, requestCase{name: model + " effort " + effort, model: model, path: "/responses", tokenField: "max_output_tokens", tools: true, thinking: true, stream: true, reasoningEffort: effort})
		}
	}
	for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max"} {
		tests = append(tests, requestCase{name: "gpt-6.1-sol effort " + effort, model: "gpt-6.1-sol", path: "/responses", tokenField: "max_output_tokens", tools: true, thinking: true, stream: true, reasoningEffort: effort})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotPath string
			var got map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if !tc.stream && r.URL.Path == "/chat/completions" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop","index":0}],"usage":{}}`))
					return
				}
				if !tc.stream && r.URL.Path == "/responses" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"r1","status":"completed","output":[],"usage":{}}`))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if r.URL.Path == "/responses" {
					_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
					return
				}
				_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\ndata: [DONE]\n\n"))
			}))
			defer server.Close()

			providerOptions := []options.Option[openai.Provider]{openai.WithModel(tc.model)}
			if tc.publicURL {
				providerOptions = append(providerOptions, openai.WithHTTPClient(&http.Client{Transport: redirectTransport{target: server.URL}}))
			} else {
				providerOptions = append(providerOptions, openai.WithBaseURL(server.URL))
			}
			if tc.responsesAPI != nil {
				providerOptions = append(providerOptions, openai.WithResponsesAPI(*tc.responsesAPI))
			} else if tc.path == "/responses" {
				providerOptions = append(providerOptions, openai.WithResponsesAPI(true))
			}
			p := openai.NewProvider("test-key", providerOptions...)

			req := llm.CompletionRequest{Stream: tc.stream, MaxTokens: 42}
			if tc.tools {
				req.Tools = []llm.Tool{tool}
			}
			req.Thinking.Enabled = tc.thinking
			if tc.reasoningEffort != "" {
				req.Options = llm.ModelOptions{"reasoning_effort": tc.reasoningEffort}
			}
			var streamErr error
			for _, err := range p.Complete(t.Context(), req) {
				if err != nil {
					streamErr = err
				}
			}
			if streamErr != nil {
				t.Fatalf("Complete: %v", streamErr)
			}
			if gotPath != tc.path {
				t.Fatalf("request path = %q, want %q", gotPath, tc.path)
			}
			if got["model"] != tc.model {
				t.Fatalf("model = %#v, want %q", got["model"], tc.model)
			}
			if tc.stream && got["stream"] != true {
				t.Fatalf("stream = %#v, want true", got["stream"])
			}
			if !tc.stream && got["stream"] != nil && got["stream"] != false {
				t.Fatalf("stream = %#v, want false or omitted for non-stream request", got["stream"])
			}
			if tc.tokenField != "" {
				if got[tc.tokenField] != float64(42) {
					t.Fatalf("%s = %#v, want 42; body=%v", tc.tokenField, got[tc.tokenField], got)
				}
				other := "max_tokens"
				if other == tc.tokenField {
					other = "max_completion_tokens"
				}
				if _, ok := got[other]; ok {
					t.Fatalf("unexpected %s: %#v", other, got[other])
				}
			}
			if tc.path == "/chat/completions" && tc.tools {
				tools, _ := got["tools"].([]any)
				if len(tools) != 1 {
					t.Fatalf("tools = %#v, want one", got["tools"])
				}
				toolWire, _ := tools[0].(map[string]any)
				function, _ := toolWire["function"].(map[string]any)
				if toolWire["type"] != "function" || function["name"] != "read" {
					t.Fatalf("tool serialization = %#v", tools[0])
				}
				parameters, _ := function["parameters"].(map[string]any)
				if parameters["type"] != "object" {
					t.Fatalf("tool schema = %#v", function["parameters"])
				}
			}
			if tc.path == "/responses" {
				if tc.tools && got["parallel_tool_calls"] != true {
					t.Fatalf("parallel_tool_calls = %#v, want true", got["parallel_tool_calls"])
				}
				if !tc.tools {
					if _, ok := got["parallel_tool_calls"]; ok {
						t.Fatalf("parallel_tool_calls = %#v, want omitted", got["parallel_tool_calls"])
					}
				}
				wantEffort := tc.reasoningEffort
				if wantEffort == "" && tc.thinking {
					wantEffort = "medium"
				}
				if wantEffort != "" {
					reasoning, _ := got["reasoning"].(map[string]any)
					if reasoning["effort"] != wantEffort {
						t.Fatalf("reasoning = %#v, want %s", got["reasoning"], wantEffort)
					}
				}
			}
		})
	}
}

func TestOpenAIPlanningEnumWireValues(t *testing.T) {
	t.Parallel()
	checks := map[string]string{
		"endpoint chat": openai.EndpointKinds.ENDPOINTCHATCOMPLETIONS.String(), "endpoint responses": openai.EndpointKinds.ENDPOINTRESPONSES.String(),
		"token max_tokens": openai.TokenLimitFields.TOKENLIMITMAXTOKENS.String(), "token max_completion": openai.TokenLimitFields.TOKENLIMITMAXCOMPLETIONTOKENS.String(), "token max_output": openai.TokenLimitFields.TOKENLIMITMAXOUTPUTTOKENS.String(),
		"reasoning low": openai.ReasoningEfforts.REASONINGEFFORTLOW.String(), "reasoning medium": openai.ReasoningEfforts.REASONINGEFFORTMEDIUM.String(), "reasoning high": openai.ReasoningEfforts.REASONINGEFFORTHIGH.String(), "reasoning xhigh": openai.ReasoningEfforts.REASONINGEFFORTXHIGH.String(), "reasoning max": openai.ReasoningEfforts.REASONINGEFFORTMAX.String(),
	}
	wants := map[string]string{"endpoint chat": "chat_completions", "endpoint responses": "responses", "token max_tokens": "max_tokens", "token max_completion": "max_completion_tokens", "token max_output": "max_output_tokens", "reasoning low": "low", "reasoning medium": "medium", "reasoning high": "high", "reasoning xhigh": "xhigh", "reasoning max": "max"}
	for name, got := range checks {
		if got != wants[name] {
			t.Fatalf("%s = %q, want %q", name, got, wants[name])
		}
	}
}

type redirectTransport struct{ target string }

func (transport redirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	redirected := request.Clone(request.Context())
	redirected.URL.Scheme = "http"
	redirected.URL.Host = strings.TrimPrefix(transport.target, "http://")
	redirected.URL.Path = strings.TrimPrefix(redirected.URL.Path, "/v1")
	return http.DefaultTransport.RoundTrip(redirected)
}
