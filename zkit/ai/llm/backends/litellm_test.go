package backends_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

const gatewayAlias = "gpt-5/Company Alias-V7"
const gatewayKey = "ordinary-gateway-key"

func TestLiteLLMRegistryConstructionAndDiscovery(t *testing.T) {
	t.Parallel()
	builtin, ok := backends.Builtin(llm.LLMProviders.LITELLM.String())
	if !ok || builtin.AdapterType != backends.AdapterTypes.LITELLM || builtin.BaseURL != "http://localhost:4000" || builtin.DefaultModel != "" || len(builtin.SeedModels) != 0 || !builtin.RequiresKey() || !builtin.Builtin || !builtin.Enabled {
		t.Fatalf("LiteLLM catalogue = %+v", builtin)
	}
	for _, custom := range []bool{false, true} {
		name := builtin.Name
		if custom {
			name = "company-gateway"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/proxy/team/v1/models" || r.Header.Get("Authorization") != "Bearer "+gatewayKey || r.Header.Get("Anthropic-Version") != "" || r.Header.Get("Client_version") != "" {
					t.Errorf("discovery method/path/auth = %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"`+gatewayAlias+`"}]}`)
			}))
			t.Cleanup(server.Close)
			def := builtin
			def.Name, def.BaseURL, def.Builtin = name, server.URL+"/proxy/team", !custom
			reg := backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}), backends.WithSettingsService(fakeVault{keys: map[string]string{name: gatewayKey}}))
			if custom {
				reg = backends.NewRegistry(backends.WithStore(newFakeStore()), backends.WithSettingsService(fakeVault{keys: map[string]string{name: gatewayKey}}))
				if err := reg.UpsertProvider(t.Context(), def); err != nil {
					t.Fatal(err)
				}
			}
			models, err := reg.FetchModels(t.Context(), name)
			if err != nil || !slices.Equal(models, []string{gatewayAlias}) {
				t.Fatalf("FetchModels = %v, %v", models, err)
			}
			provider, err := reg.Build(t.Context(), name, gatewayAlias)
			if err != nil {
				t.Fatal(err)
			}
			if provider.Name() != name {
				t.Fatalf("Name = %q", provider.Name())
			}
		})
	}
}

func TestLiteLLMRegistryToolLoop(t *testing.T) {
	t.Parallel()
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLRESPONSES} {
		t.Run(protocol.String(), func(t *testing.T) {
			t.Parallel()
			responses := protocol == llm.ModelProtocols.PROTOCOLRESPONSES
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := "/proxy/v1/chat/completions"
				if responses {
					path = "/proxy/v1/responses"
				}
				if r.URL.Path != path || r.Header.Get("Authorization") != "Bearer "+gatewayKey {
					t.Errorf("request path/auth = %s %q", r.URL.Path, r.Header.Get("Authorization"))
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- body
				if body["model"] != gatewayAlias || body["cache_prompt"] != nil || body["chat_template_kwargs"] != nil {
					t.Errorf("request model/extensions = %#v", body)
				}
				tools, _ := body["tools"].([]any)
				if len(tools) != 1 {
					t.Errorf("tools = %#v", tools)
				} else {
					tool := tools[0].(map[string]any)
					if !responses {
						tool = tool["function"].(map[string]any)
					}
					parameters := tool["parameters"].(map[string]any)
					properties := parameters["properties"].(map[string]any)
					if tool["name"] != "lookup" || parameters["type"] != "object" || properties["query"].(map[string]any)["type"] != "string" {
						t.Errorf("tool schema = %#v", tool)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				followup := body["messages"] != nil && len(body["messages"].([]any)) == 3
				if responses {
					followup = len(body["input"].([]any)) == 3
				}
				if responses {
					if followup {
						_, _ = io.WriteString(w, `{"id":"r2","status":"completed","output":[{"type":"message","id":"m2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}]}`)
					} else {
						_, _ = io.WriteString(w, `{"id":"r1","status":"completed","output":[{"type":"function_call","id":"fc1","call_id":"call1","name":"lookup","arguments":"{\"query\":\"test\"}","status":"completed"}]}`)
					}
				} else {
					if followup {
						_, _ = io.WriteString(w, `{"id":"c2","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
					} else {
						_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"test\"}"}}]},"finish_reason":"tool_calls"}]}`)
					}
				}
			}))
			t.Cleanup(server.Close)
			provider, err := backends.NewRegistry().BuildWithConfig(t.Context(), "litellm", backends.BuildConfig{Model: gatewayAlias, APIKey: gatewayKey, BaseURL: server.URL + "/proxy", Protocol: protocol})
			if err != nil {
				t.Fatal(err)
			}
			req := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "lookup"}}, Tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "lookup", Parameters: llm.Schema{Type: "object", Properties: map[string]llm.Schema{"query": {Type: "string"}}}}}}}
			var calls []llm.ToolCall
			for chunk, err := range provider.Complete(t.Context(), req) {
				if err != nil {
					t.Fatal(err)
				}
				calls = append(calls, chunk.Clone().ToolCalls...)
			}
			if len(calls) != 1 || calls[0].ID != "call1" || calls[0].Function.Name != "lookup" || calls[0].Function.Arguments != `{"query":"test"}` {
				t.Fatalf("calls = %+v", calls)
			}
			<-requests
			req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}, llm.Message{Role: llm.RoleTool, ToolCallID: calls[0].ID, Content: "result"})
			var answer strings.Builder
			for chunk, err := range provider.Complete(t.Context(), req) {
				if err != nil {
					t.Fatal(err)
				}
				answer.WriteString(chunk.Content)
			}
			if answer.String() != "done" {
				t.Fatalf("answer = %q", answer.String())
			}
			followup := <-requests
			field := "messages"
			if responses {
				field = "input"
			}
			items := followup[field].([]any)
			result := items[2].(map[string]any)
			if responses {
				if result["type"] != "function_call_output" || result["call_id"] != "call1" || result["output"] != "result" {
					t.Fatalf("followup = %#v", items)
				}
			} else if result["role"] != "tool" || result["tool_call_id"] != "call1" || result["content"] != "result" {
				t.Fatalf("followup = %#v", items)
			}
		})
	}
}

func TestLiteLLMBoundaries(t *testing.T) {
	t.Setenv("LITELLM_API_KEY", "ambient-not-a-credential")
	t.Setenv("OPENAI_API_KEY", "ambient-not-a-credential")
	builtin, _ := backends.Builtin("litellm")
	custom := builtin
	custom.Name, custom.Builtin, custom.APIKeyRequired = "custom-gateway", false, false
	for _, def := range []backends.ProviderDefinition{builtin, custom} {
		reg := backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))
		if _, err := reg.Build(t.Context(), def.Name, gatewayAlias); !errors.Is(err, llm.ErrInvalidAPIKey) {
			t.Fatalf("%s missing key = %v", def.Name, err)
		}
		if _, err := reg.FetchModels(t.Context(), def.Name); !errors.Is(err, llm.ErrInvalidAPIKey) {
			t.Fatalf("%s discovery missing key = %v", def.Name, err)
		}
	}
	for _, raw := range []string{"ftp://gateway/proxy", "file:///tmp/gateway", "//gateway/path", "http://user:secret@gateway", "https://gateway?secret=canary", "https://gateway?", "https://gateway/#canary", "https://gateway/#", "http://[invalid"} {
		t.Run(raw, func(t *testing.T) {
			reg := backends.NewRegistry()
			_, err := reg.BuildWithConfig(t.Context(), "litellm", backends.BuildConfig{APIKey: gatewayKey, BaseURL: raw})
			if !errors.Is(err, backends.ErrInvalidProviderDef) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "canary") {
				t.Fatalf("URL error = %v", err)
			}
			def := builtin
			def.BaseURL = raw
			reg = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}), backends.WithSettingsService(fakeVault{keys: map[string]string{"litellm": gatewayKey}}))
			if _, err := reg.FetchModels(t.Context(), "litellm"); !errors.Is(err, backends.ErrInvalidProviderDef) {
				t.Fatalf("discovery URL = %v", err)
			}
		})
	}
	reg := backends.NewRegistry()
	for _, name := range []string{"openai", "llamacpp", "anthropic", "deepseek", "gemini", "google-vertex", "openai-codex", "claude-code"} {
		_, err := reg.BuildWithConfig(t.Context(), name, backends.BuildConfig{APIKey: gatewayKey, Protocol: llm.ModelProtocols.PROTOCOLRESPONSES})
		if !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("%s protocol override = %v", name, err)
		}
	}
	invalid, _ := llm.ParseModelProtocol("invalid")
	if _, err := reg.BuildWithConfig(t.Context(), "litellm", backends.BuildConfig{APIKey: gatewayKey, Protocol: invalid}); !errors.Is(err, backends.ErrInvalidProviderDef) {
		t.Fatalf("invalid protocol = %v", err)
	}
}

func TestLiteLLMAliasesDoNotBorrowNativeMetadata(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"openai":{"models":{"gpt-4o":{"id":"gpt-4o","tool_call":true,"reasoning":true,"modalities":{"input":["image","video"]},"limit":{"context":999999},"cost":{"input":10,"output":20}}}},"litellm":{"models":{"gpt-4o":{"id":"gpt-4o","tool_call":true,"reasoning":true,"modalities":{"input":["image","video"]},"limit":{"context":999999},"cost":{"input":10,"output":20}}}}}`)
	}))
	t.Cleanup(server.Close)
	source := modelsdev.New(cache.NewMemoryCache[string, modelsdev.Snapshot](), modelsdev.WithBaseURL(server.URL))
	if _, ok := source.Lookup(t.Context(), "openai", "gpt-4o"); !ok {
		t.Fatal("native fixture not loaded")
	}
	builtin, _ := backends.Builtin("litellm")
	custom := builtin
	custom.Name, custom.Builtin, custom.ContextWindow = "gateway-constrained", false, 12345
	reg := backends.NewRegistry(backends.WithModelsDevSource(source), backends.WithProviderDefinitions([]backends.ProviderDefinition{builtin, custom}))
	for _, name := range []string{"litellm", "gateway-constrained"} {
		wantWindow := 0
		if name == custom.Name {
			wantWindow = custom.ContextWindow
		}
		for _, model := range []string{"gpt-4o", "claude-sonnet-4-6", "gemini-2.5-pro"} {
			if reg.Capabilities(name, model) != (llm.ModelCapabilities{}) || reg.ResolveCapabilities(t.Context(), name, model) != (llm.ModelCapabilities{}) || reg.ResolveCapabilitiesCached(name, model) != (llm.ModelCapabilities{}) {
				t.Fatalf("%s/%s borrowed capabilities", name, model)
			}
			if reg.ContextWindow(name, model) != wantWindow || reg.ResolveContextWindow(t.Context(), name, "", model) != wantWindow || reg.ResolveContextWindowCached(name, model) != wantWindow {
				t.Fatalf("%s/%s context constraint lost or inferred", name, model)
			}
			if _, _, ok := reg.Cost(name, model); ok {
				t.Fatal("inferred cost")
			}
			if _, _, ok := reg.ResolveCost(t.Context(), name, model); ok {
				t.Fatal("inferred live cost")
			}
			if _, _, ok := reg.ResolveCostCached(name, model); ok {
				t.Fatal("inferred cached cost")
			}
			if estimate, ok := reg.EstimateCost(t.Context(), name, model, llm.Usage{PromptTokens: 100}); ok || !estimate.Incomplete {
				t.Fatalf("inferred estimate = %+v, %v", estimate, ok)
			}
		}
	}
	if reg.ResolveCapabilitiesCached("litellm", "gpt-4o").SupportsTools || !backends.NewRegistry(backends.WithModelsDevSource(source)).ResolveCapabilitiesCached("openai", "gpt-4o").SupportsTools {
		t.Fatal("native route changed")
	}
}

func TestLiteLLMExplicitProtocolSelection(t *testing.T) {
	t.Parallel()
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLRESPONSES, llm.ModelProtocols.PROTOCOLMESSAGES, llm.ModelProtocols.PROTOCOLGENERATECONTENT} {
		t.Run(protocol.String(), func(t *testing.T) {
			t.Parallel()
			provider, err := backends.NewRegistry().BuildWithConfig(t.Context(), "litellm", backends.BuildConfig{Model: gatewayAlias, APIKey: gatewayKey, Protocol: protocol})
			if err != nil {
				t.Fatal(err)
			}
			selected, ok := provider.(interface{ Protocol() llm.ModelProtocol })
			if !ok || selected.Protocol() != protocol {
				t.Fatalf("protocol selection = %T", provider)
			}
		})
	}
}
