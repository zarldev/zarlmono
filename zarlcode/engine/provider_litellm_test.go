package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestBuildLiteLLMProvider(t *testing.T) {
	t.Parallel()
	const alias = "gpt-5/Production Alias"
	const key = "ordinary-user-key"
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLRESPONSES} {
		t.Run(protocol.String(), func(t *testing.T) {
			t.Parallel()
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("missing gateway credential")
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/gateway/v1/models" {
					_, _ = io.WriteString(w, `{"data":[{"id":"`+alias+`"}]}`)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != alias {
					t.Errorf("model = %v", body["model"])
				}
				requests <- body
				if protocol == llm.ModelProtocols.PROTOCOLRESPONSES {
					if r.URL.Path != "/gateway/v1/responses" {
						t.Errorf("Responses path = %s", r.URL.Path)
					}
					_, _ = io.WriteString(w, `{"id":"r1","status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}]}`)
					return
				}
				if r.URL.Path != "/gateway/v1/chat/completions" {
					t.Errorf("default Chat path = %s", r.URL.Path)
				}
				messages, _ := body["messages"].([]any)
				if len(messages) == 1 {
					_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
					return
				}
				if len(messages) != 3 {
					t.Errorf("followup = %#v", messages)
				} else {
					tool := messages[2].(map[string]any)
					if tool["role"] != "tool" || tool["tool_call_id"] != "call1" || tool["content"] != "result" {
						t.Errorf("tool result = %#v", tool)
					}
				}
				_, _ = io.WriteString(w, `{"id":"c2","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
			}))
			t.Cleanup(server.Close)
			def, _ := backends.Builtin("litellm")
			def.BaseURL = server.URL + "/gateway"
			reg := backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}), backends.WithSettingsService(liteLLMKeys{}))
			models, err := reg.FetchModels(t.Context(), "litellm")
			if err != nil || !slices.Equal(models, []string{alias}) {
				t.Fatalf("models = %v, %v", models, err)
			}
			provider, err := engine.BuildProvider(t.Context(), reg, nil, engine.ProviderSpec{Name: "litellm", Model: alias, APIKey: key, BaseURL: server.URL + "/gateway", Protocol: protocol})
			if err != nil {
				t.Fatal(err)
			}
			if provider.Name() != "litellm" {
				t.Fatalf("Name = %q", provider.Name())
			}
			req := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "lookup"}}, Tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "lookup", Parameters: llm.Schema{Type: "object"}}}}}
			var answer strings.Builder
			var calls []llm.ToolCall
			for chunk, err := range provider.Complete(t.Context(), req) {
				if err != nil {
					t.Fatal(err)
				}
				answer.WriteString(chunk.Content)
				calls = append(calls, chunk.Clone().ToolCalls...)
			}
			first := <-requests
			tools, _ := first["tools"].([]any)
			if len(tools) != 1 {
				t.Fatalf("tools = %#v", tools)
			}
			if protocol == llm.ModelProtocols.PROTOCOLRESPONSES {
				if answer.String() != "done" {
					t.Fatalf("Responses content = %q", answer.String())
				}
				return
			}
			if len(calls) != 1 || calls[0].Function.Name != "lookup" {
				t.Fatalf("calls = %+v", calls)
			}
			req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}, llm.Message{Role: llm.RoleTool, ToolCallID: calls[0].ID, Content: "result"})
			for chunk, err := range provider.Complete(t.Context(), req) {
				if err != nil {
					t.Fatal(err)
				}
				answer.WriteString(chunk.Content)
			}
			<-requests
			if answer.String() != "done" {
				t.Fatalf("tool-loop content = %q", answer.String())
			}
		})
	}
}

func TestBuildProviderRejectsNonGatewayProtocolOverride(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"openai", "llamacpp", "openai-codex", "claude-code"} {
		_, err := engine.BuildProvider(t.Context(), backends.NewRegistry(), nil, engine.ProviderSpec{Name: name, APIKey: "ordinary-key", Protocol: llm.ModelProtocols.PROTOCOLRESPONSES})
		if !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("%s protocol error = %v", name, err)
		}
	}
}

type liteLLMKeys struct{}

func (liteLLMKeys) GetKey(context.Context, string) (string, error) {
	return "ordinary-user-key", nil
}
