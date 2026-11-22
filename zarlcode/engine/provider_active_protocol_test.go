package engine_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestBuildActiveLiteLLMResponsesToolLoop(t *testing.T) {
	t.Parallel()
	const alias = "gpt-5/Production Alias"
	const key = "ordinary-user-key"
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gateway/v1/responses" {
			t.Errorf("Responses route = %s %s", r.Method, r.URL.Path)
			http.Error(w, "wrong protocol route", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("missing fallback gateway credential")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid request JSON", http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		input, _ := body["input"].([]any)
		if len(input) == 1 {
			_, _ = io.WriteString(w, `{"id":"r1","status":"completed","output":[{"type":"function_call","id":"fc1","call_id":"call1","name":"lookup","status":"completed","arguments":"{}"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"r2","status":"completed","output":[{"type":"message","id":"m2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}]}`)
	}))
	t.Cleanup(server.Close)
	settings := activeProtocolSettings(t)
	fallback := engine.ProviderSpec{Name: "litellm", Model: alias, BaseURL: server.URL + "/gateway", APIKey: key, Protocol: llm.ModelProtocols.PROTOCOLRESPONSES}
	// An explicit pin to the fallback backend must retain its runtime overrides.
	if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, prefs.ModelSelection{Provider: "litellm", Model: alias}); err != nil {
		t.Fatal(err)
	}
	provider, spec, err := settings.BuildActive(t.Context(), fallback)
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "litellm" {
		t.Fatalf("provider name = %q", provider.Name())
	}
	request := llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "lookup"}},
		Tools:    []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "lookup", Parameters: llm.Schema{Type: "object"}}}},
	}
	var calls []llm.ToolCall
	for chunk, err := range provider.Complete(t.Context(), request) {
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, chunk.Clone().ToolCalls...)
	}
	if spec != fallback {
		t.Fatalf("resolved spec = %#v, want %#v", spec, fallback)
	}
	if len(calls) != 1 || calls[0].ID != "call1" || calls[0].Function.Name != "lookup" || calls[0].Function.Arguments != "{}" {
		t.Fatalf("Responses calls = %#v", calls)
	}
	first := <-requests
	assertActiveResponsesBody(t, first, alias)
	input, _ := first["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("initial input = %#v", input)
	}
	user, _ := input[0].(map[string]any)
	content, _ := user["content"].([]any)
	if user["role"] != "user" || len(content) != 1 {
		t.Fatalf("user input = %#v", user)
	}
	text, _ := content[0].(map[string]any)
	if text["type"] != "input_text" || text["text"] != "lookup" {
		t.Fatalf("user text = %#v", text)
	}
	request.Messages = append(request.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: calls},
		llm.Message{Role: llm.RoleTool, ToolCallID: calls[0].ID, Content: "result"},
	)
	var answer strings.Builder
	for chunk, err := range provider.Complete(t.Context(), request) {
		if err != nil {
			t.Fatal(err)
		}
		answer.WriteString(chunk.Content)
	}
	if answer.String() != "done" {
		t.Fatalf("tool-loop answer = %q", answer.String())
	}
	followup := <-requests
	assertActiveResponsesBody(t, followup, alias)
	input, _ = followup["input"].([]any)
	if len(input) != 3 || !reflect.DeepEqual(input[0], user) {
		t.Fatalf("followup input (duplicated projection?) = %#v", input)
	}
	call, _ := input[1].(map[string]any)
	result, _ := input[2].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call1" || call["name"] != "lookup" || call["arguments"] != "{}" || result["type"] != "function_call_output" || result["call_id"] != "call1" || result["output"] != "result" {
		t.Fatalf("Responses tool binding = %#v", input)
	}
}

func assertActiveResponsesBody(t *testing.T, body map[string]any, alias string) {
	t.Helper()
	if body["model"] != alias || body["stream"] != false || body["store"] != false {
		t.Fatalf("Responses controls = %#v", body)
	}
	if _, present := body["messages"]; present {
		t.Fatal("Chat messages leaked into Responses body")
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
	tool, _ := tools[0].(map[string]any)
	parameters, _ := tool["parameters"].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "lookup" || parameters["type"] != "object" {
		t.Fatalf("Responses tool schema = %#v", tool)
	}
}

func TestBuildActiveRejectsNonLiteLLMProtocolOverride(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"openai", "llamacpp", "openai-codex", "claude-code"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			settings := activeProtocolSettings(t)
			_, spec, err := settings.BuildActive(t.Context(), engine.ProviderSpec{Name: name, APIKey: "fixture-key", Protocol: llm.ModelProtocols.PROTOCOLRESPONSES})
			if !errors.Is(err, backends.ErrInvalidProviderDef) {
				t.Fatalf("BuildActive(%s) protocol error = %v, spec = %#v", name, err, spec)
			}
		})
	}
}

func TestBuildActiveDifferentProviderDoesNotInheritProtocol(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("selected provider route = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"selected"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	settings := activeProtocolSettings(t)
	def, _ := backends.Builtin("llamacpp")
	def.BaseURL = server.URL + "/v1"
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))
	if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, prefs.ModelSelection{Provider: "llamacpp", Model: "selected-model"}); err != nil {
		t.Fatal(err)
	}
	provider, spec, err := settings.BuildActive(t.Context(), engine.ProviderSpec{Name: "litellm", Model: "fallback-model", BaseURL: "http://127.0.0.1:1/unused", APIKey: "fallback-key", Protocol: llm.ModelProtocols.PROTOCOLRESPONSES})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "llamacpp" || spec.Model != "selected-model" || spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || spec.BaseURL != "" || spec.APIKey != "" {
		t.Fatalf("fallback overrides leaked into selected provider: %#v", spec)
	}
	var answer strings.Builder
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
		if err != nil {
			t.Fatal(err)
		}
		answer.WriteString(chunk.Content)
	}
	body := <-requests
	messages, _ := body["messages"].([]any)
	if answer.String() != "selected" || body["model"] != "selected-model" || len(messages) != 1 {
		t.Fatalf("selected provider answer = %q, body = %#v", answer.String(), body)
	}
	if _, present := body["input"]; present {
		t.Fatal("fallback Responses input leaked into selected Chat body")
	}
}

func activeProtocolSettings(t *testing.T) *engine.Settings {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return engine.NewSettings(store, nil, nil, t.TempDir())
}
