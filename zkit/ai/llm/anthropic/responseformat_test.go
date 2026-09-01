package anthropic_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestProviderAppliesResponseFormatAndThinking(t *testing.T) {
	tests := []struct {
		name string
		req  llm.CompletionRequest
		want func(*testing.T, map[string]any)
	}{
		{
			name: "JSON schema",
			req: llm.CompletionRequest{ResponseFormat: llm.ResponseFormat{
				Type: llm.ResponseFormatJSONSchema,
				Schema: llm.SchemaFromMap(map[string]any{
					"type": "object", "properties": map[string]any{"verdict": map[string]any{"type": "string"}},
				}),
			}},
			want: func(t *testing.T, body map[string]any) {
				format := body["output_config"].(map[string]any)["format"].(map[string]any)
				if _, ok := format["schema"].(map[string]any)["properties"]; !ok {
					t.Errorf("schema not forwarded: %#v", format)
				}
			},
		},
		{
			name: "JSON object no-op",
			req:  llm.CompletionRequest{ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}},
			want: func(t *testing.T, body map[string]any) {
				if _, ok := body["output_config"]; ok {
					t.Errorf("unexpected output_config: %#v", body["output_config"])
				}
			},
		},
		{
			name: "thinking clamps budget and grows max tokens",
			req:  llm.CompletionRequest{MaxTokens: 500, Temperature: 0.7, Thinking: llm.ThinkingConfig{Enabled: true}},
			want: func(t *testing.T, body map[string]any) {
				thinking := body["thinking"].(map[string]any)
				if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(1024) {
					t.Errorf("thinking = %#v", thinking)
				}
				if body["max_tokens"].(float64) <= 1024 {
					t.Errorf("max_tokens = %v, want greater than budget", body["max_tokens"])
				}
				if _, ok := body["temperature"]; ok {
					t.Errorf("temperature sent with thinking: %#v", body["temperature"])
				}
			},
		},
		{
			name: "explicit thinking budget preserves ample max tokens",
			req:  llm.CompletionRequest{MaxTokens: 20000, Thinking: llm.ThinkingConfig{Enabled: true, BudgetTokens: 8000}},
			want: func(t *testing.T, body map[string]any) {
				if got := body["thinking"].(map[string]any)["budget_tokens"]; got != float64(8000) {
					t.Errorf("budget_tokens = %v, want 8000", got)
				}
				if got := body["max_tokens"]; got != float64(20000) {
					t.Errorf("max_tokens = %v, want 20000", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := captureAnthropicRequest(t, tc.req)
			tc.want(t, body)
		})
	}
}

func captureAnthropicRequest(t *testing.T, req llm.CompletionRequest) map[string]any {
	t.Helper()
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0}}`))
	}))
	t.Cleanup(server.Close)

	provider, err := anthropic.NewProvider("test-key", anthropic.WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	req.Messages = []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
	for _, completeErr := range provider.Complete(t.Context(), req) {
		if completeErr != nil {
			t.Fatalf("Complete: %v", completeErr)
		}
	}
	return <-requests
}
