package litellm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

func TestProtocolToolLoops(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		for _, stream := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/stream=%t", fixture.protocol, stream), func(t *testing.T) {
				t.Parallel()
				requests := make(chan map[string]any, 2)
				var count atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/gateway"+fixture.requestPath(stream) {
						t.Errorf("request = %s %s, want POST /gateway%s", r.Method, r.URL.Path, fixture.requestPath(stream))
					}
					assertAuth(t, r, fixture.protocol)
					if fixture.protocol == llm.ModelProtocols.PROTOCOLGENERATECONTENT && r.URL.Query().Get("alt") != map[bool]string{true: "sse", false: ""}[stream] {
						t.Errorf("query = %q", r.URL.RawQuery)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode: %v", err)
					}
					requests <- body
					fixture.serve(w, stream, count.Add(1) == 2)
				}))
				t.Cleanup(server.Close)
				provider := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL+"/gateway"), litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(server.Client()))
				if provider.Name() != "litellm" || provider.Protocol() != fixture.protocol {
					t.Fatal("adapter/protocol identity changed")
				}
				req := providertest.RequestWithTool("echo hi")
				req.Stream = stream
				before := requestJSON(t, req)
				assistant, metadata := collect(t, provider.Complete(t.Context(), req))
				if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call_1" || assistant.ToolCalls[0].Function.Name != "echo" {
					t.Fatalf("calls = %+v", assistant.ToolCalls)
				}
				if assistant.ToolCalls[0].Function.Arguments != `{"text":"hi"}` {
					t.Errorf("arguments = %q", assistant.ToolCalls[0].Function.Arguments)
				}
				if !metadata.UsageReported || metadata.Usage.PromptTokens != 5 || metadata.Usage.CompletionTokens != 3 {
					t.Errorf("metadata = %+v", metadata)
				}
				assertTools(t, fixture.protocol, <-requests)
				if before != requestJSON(t, req) {
					t.Fatal("initial request mutated")
				}
				req.Messages = append(req.Messages, assistant, llm.Message{Role: llm.RoleTool, ToolCallID: "call_1", Content: `{"text":"hi"}`})
				before = requestJSON(t, req)
				answer, _ := collect(t, provider.Complete(t.Context(), req))
				if answer.Content != "done" {
					t.Errorf("answer = %q", answer.Content)
				}
				assertFollowup(t, fixture.protocol, <-requests)
				if before != requestJSON(t, req) {
					t.Fatal("followup request mutated")
				}
			})
		}
	}
}

func assertAuth(t *testing.T, r *http.Request, protocol llm.ModelProtocol) {
	t.Helper()
	if r.Header.Get("X-Goog-Api-Key") != "" {
		t.Error("conflicting Google credentials")
	}
	if protocol == llm.ModelProtocols.PROTOCOLMESSAGES {
		if r.Header.Get("X-Api-Key") != "ordinary-test-key" || r.Header.Get("Authorization") != "" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Error("incorrect Messages authentication/version")
		}
	} else if r.Header.Get("Authorization") != "Bearer ordinary-test-key" || r.Header.Get("X-Api-Key") != "" {
		t.Error("incorrect gateway Bearer authentication")
	}
}

func requestJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertTools(t *testing.T, protocol llm.ModelProtocol, body map[string]any) {
	t.Helper()
	if protocol != llm.ModelProtocols.PROTOCOLGENERATECONTENT && body["model"] != alias {
		t.Errorf("model = %#v", body["model"])
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool := tools[0].(map[string]any)
	schemaKey := "parameters"
	switch protocol {
	case llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS:
		tool = tool["function"].(map[string]any)
	case llm.ModelProtocols.PROTOCOLMESSAGES:
		schemaKey = "input_schema"
	case llm.ModelProtocols.PROTOCOLGENERATECONTENT:
		tool = tool["functionDeclarations"].([]any)[0].(map[string]any)
		schemaKey = "parametersJsonSchema"
	}
	if tool["name"] != "echo" {
		t.Errorf("function name = %#v", tool["name"])
	}
	schema, ok := tool[schemaKey].(map[string]any)
	if !ok {
		t.Fatalf("schema = %#v", tool[schemaKey])
	}
	if !strings.EqualFold(fmt.Sprint(schema["type"]), "object") || schema["properties"].(map[string]any)["text"] == nil {
		t.Errorf("schema = %#v", schema)
	}
}

func assertFollowup(t *testing.T, protocol llm.ModelProtocol, body map[string]any) {
	t.Helper()
	switch protocol {
	case llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS:
		messages := body["messages"].([]any)
		assistant := messages[1].(map[string]any)
		result := messages[2].(map[string]any)
		if assistant["tool_calls"].([]any)[0].(map[string]any)["id"] != "call_1" || result["tool_call_id"] != "call_1" || result["role"] != "tool" || result["content"] != `{"text":"hi"}` {
			t.Errorf("unpaired Chat followup: %#v", messages)
		}
	case llm.ModelProtocols.PROTOCOLRESPONSES:
		input := body["input"].([]any)
		assertJSONEqual(t, input[1], signedReasoning)
		assertJSONEqual(t, input[2], `{"type":"function_call","call_id":"call_1","name":"echo","arguments":"{\"text\":\"hi\"}"}`)
		result := input[3].(map[string]any)
		if result["type"] != "function_call_output" || result["call_id"] != "call_1" || result["output"] != `{"text":"hi"}` {
			t.Errorf("Responses result = %#v", result)
		}
	case llm.ModelProtocols.PROTOCOLMESSAGES:
		messages := body["messages"].([]any)
		blocks := messages[1].(map[string]any)["content"].([]any)
		assertJSONEqual(t, blocks[0], anthropicThinking)
		if blocks[1].(map[string]any)["id"] != "call_1" {
			t.Error("missing native tool use")
		}
		result := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
		if result["type"] != "tool_result" || result["tool_use_id"] != "call_1" || result["content"].([]any)[0].(map[string]any)["text"] != `{"text":"hi"}` {
			t.Errorf("Messages result = %#v", result)
		}
	case llm.ModelProtocols.PROTOCOLGENERATECONTENT:
		contents := body["contents"].([]any)
		assertJSONEqual(t, contents[1].(map[string]any)["parts"], googleParts)
		result := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
		assertJSONEqual(t, result["response"], `{"result":"{\"text\":\"hi\"}"}`)
		if result["id"] != "call_1" || result["name"] != "echo" {
			t.Errorf("Gemini result = %#v", result)
		}
	}
}

func assertJSONEqual(t *testing.T, got any, want string) {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(want), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, decoded) {
		t.Errorf("JSON mismatch\ngot: %s\nwant: %s", requestJSON(t, got), want)
	}
}
