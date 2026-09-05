package openai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestProviderPlansRequestsOnTheWire(t *testing.T) {
	t.Parallel()

	tool := llm.Tool{Type: "function", Function: llm.ToolFunction{Name: "read", Parameters: llm.SchemaFromMap(map[string]any{"type": "object"})}}
	tests := []struct {
		name, model, path, tokenField string
		tools, thinking, stream       bool
		reasoningEffort               string
		wantErr                       bool
	}{
		{name: "ordinary chat", model: "gpt-4o-mini", path: "/chat/completions", tokenField: "max_tokens", stream: true},
		{name: "ordinary chat with tools", model: "gpt-4o-mini", path: "/chat/completions", tokenField: "max_tokens", tools: true, stream: true},
		{name: "o3 without tools", model: "o3", path: "/chat/completions", tokenField: "max_completion_tokens", stream: true},
		{name: "gpt 5.6 without tools", model: "gpt-5.6", path: "/responses", stream: true},
		{name: "gpt 5.6 rejects non-stream tools", model: "gpt-5.6", tools: true, wantErr: true},
		{name: "gpt 6 astra rejects non-stream tools", model: "gpt-6-astra", tools: true, wantErr: true},
		{name: "gpt 6 astra non-stream without tools", model: "gpt-6-astra", path: "/chat/completions", tokenField: "max_completion_tokens"},
		{name: "o3 mini tools remain chat", model: "o3-mini", path: "/chat/completions", tokenField: "max_completion_tokens", tools: true, stream: true},
		{name: "gpt 5 tools and thinking", model: "gpt-5.6-sol", path: "/responses", tools: true, thinking: true, stream: true},
		{name: "gpt 6 astra max reasoning", model: "gpt-6-astra", path: "/responses", tokenField: "max_output_tokens", tools: true, thinking: true, stream: true, reasoningEffort: "max"},
		{name: "case and space variation", model: " GPT-5.6-SOL ", path: "/responses", tools: true, stream: true},
		{name: "unknown model stays on chat", model: "company-model-v7", path: "/chat/completions", tokenField: "max_tokens", tools: true, stream: true},
		{name: "local model stays on chat", model: "llama-3.3-local", path: "/chat/completions", tokenField: "max_tokens", tools: true, stream: true},
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
				w.Header().Set("Content-Type", "text/event-stream")
				if r.URL.Path == "/responses" {
					_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
					return
				}
				_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\ndata: [DONE]\n\n"))
			}))
			defer server.Close()

			providerOptions := []options.Option[openai.Provider]{openai.WithBaseURL(server.URL), openai.WithModel(tc.model)}
			if tc.path == "/responses" {
				providerOptions = append(providerOptions, openai.WithResponsesAPI(true))
			}
			p, err := openai.NewProvider("test-key", providerOptions...)
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
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
			if tc.wantErr {
				if streamErr == nil {
					t.Fatal("Complete error = nil, want error")
				}
				if gotPath != "" {
					t.Fatalf("request path = %q, want no request", gotPath)
				}
				return
			}
			if streamErr != nil {
				t.Fatalf("Complete: %v", streamErr)
			}
			if gotPath != tc.path {
				t.Fatalf("request path = %q, want %q", gotPath, tc.path)
			}
			if tc.tokenField != "" {
				if got[tc.tokenField] != float64(42) {
					t.Fatalf("%s = %#v, want 42; body=%v", tc.tokenField, got[tc.tokenField], got)
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
