package anthropic_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestModernThinkingAndStreamRequestParity(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"claude-opus-4-7", "claude-sonnet-4-6", "claude-opus-4-6", "claude-sonnet-4-5"} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			request := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}, MaxTokens: 20000,
				Thinking:       llm.ThinkingConfig{Enabled: true, BudgetTokens: 8000},
				ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.Schema{Type: "object"}}}
			plain := captureModernAnthropic(t, model, request)
			request.Stream = true
			stream := captureModernAnthropic(t, model, request)
			delete(stream, "stream")
			if !reflect.DeepEqual(plain, stream) {
				t.Fatal("stream and nonstream request policies diverged")
			}
			thinking := plain["thinking"].(map[string]any)
			want := "adaptive"
			if model == "claude-sonnet-4-5" {
				want = "enabled"
			}
			if thinking["type"] != want {
				t.Fatalf("thinking=%v, want %s", thinking, want)
			}
			if want == "adaptive" {
				if _, present := thinking["budget_tokens"]; present {
					t.Fatal("adaptive thinking fabricated a numerical budget")
				}
			}
		})
	}
}

func TestNativeReplayAndToolSchemasPreserveWholeObjects(t *testing.T) {
	t.Parallel()
	native := `{"type":"thinking","thinking":"opaque","signature":"signed","future":{"nested":null},"omitted":null}`
	schema := llm.SchemaFromMap(map[string]any{"type": "object", "additionalProperties": false,
		"$defs":      map[string]any{"Item": map[string]any{"type": "string"}},
		"properties": map[string]any{"arg": map[string]any{"$ref": "#/$defs/Item"}}, "required": []any{"arg"}})
	body := captureModernAnthropic(t, "claude-opus-4-7", llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "go"},
			{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{{Provider: "anthropic", Format: "content_block.v1", Data: []byte(native)}}},
			{Role: llm.RoleUser, Content: "follow-up"},
		},
		Tools:          []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "probe", Parameters: schema}}},
		Thinking:       llm.ThinkingConfig{Enabled: true},
		Options:        llm.ModelOptions{"reasoning_effort": "xhigh", "service_tier": "standard_only"},
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.Schema{Type: "object"}},
	})
	blocks := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
	var expected any
	if err := json.Unmarshal([]byte(native), &expected); err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || !reflect.DeepEqual(blocks[0], expected) {
		t.Fatal("optional kind hint, unknown fields, or explicit nulls were lost on native replay")
	}
	input := body["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any)
	if input["additionalProperties"] != false || input["$defs"] == nil {
		t.Fatal("schema constraints/definitions were dropped")
	}
	output := body["output_config"].(map[string]any)
	if output["effort"] != "xhigh" || output["format"] == nil || body["service_tier"] != "standard_only" {
		t.Fatal("effort, format, or service tier overwrote one another")
	}
}

func captureModernAnthropic(t *testing.T, model string, request llm.CompletionRequest) map[string]any {
	t.Helper()
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{}}`))
		}
	}))
	t.Cleanup(server.Close)
	provider := anthropic.NewProvider("synthetic-key", anthropic.WithModel(model), anthropic.WithBaseURL(server.URL))
	for _, err := range provider.Complete(t.Context(), request) {
		if err != nil {
			t.Fatal(err)
		}
	}
	return <-requests
}
