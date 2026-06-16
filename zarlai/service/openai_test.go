package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestOpenAIChat_ToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Model != "gpt-4o" {
			t.Errorf("model = %q, want gpt-4o", body.Model)
		}
		if len(body.Tools) != 1 {
			t.Errorf("tools count = %d, want 1", len(body.Tools))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "gpt-4o",
			"choices": []map[string]any{
				{
					"index":         0,
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_abc123",
								"type": "function",
								"function": map[string]any{
									"name":      "get_state",
									"arguments": `{"entity_id":"sensor.temperature"}`,
								},
							},
						},
					},
					"logprobs": nil,
				},
			},
		})
	}))
	defer srv.Close()

	client := service.NewOpenAIClient(srv.URL, "test-key", "gpt-4o")
	result, err := client.Chat(t.Context(), []service.Message{
		{Role: "user", Content: "What's the temperature?"},
	}, []llm.Tool{
		{Type: "function", Function: llm.ToolFunction{Name: "get_state"}},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "" {
		t.Errorf("content = %q, want empty", result.Content)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(result.ToolCalls))
	}

	tc := result.ToolCalls[0]
	if tc.Function.Name != "get_state" {
		t.Errorf("tool name = %q, want get_state", tc.Function.Name)
	}

	entityID := service.Get[string](tc.Function.Arguments, "entity_id")
	if entityID != "sensor.temperature" {
		t.Errorf("entity_id = %q, want sensor.temperature", entityID)
	}
}

func TestOpenAIChat_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "gpt-4o-mini",
			"choices": []map[string]any{
				{
					"index":         0,
					"finish_reason": "stop",
					"message": map[string]any{
						"role":    "assistant",
						"content": "The temperature is 22 degrees.",
					},
					"logprobs": nil,
				},
			},
		})
	}))
	defer srv.Close()

	client := service.NewOpenAIClient(srv.URL, "test-key", "gpt-4o-mini")
	result, err := client.Chat(t.Context(), []service.Message{
		{Role: "user", Content: "What's the temperature?"},
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "The temperature is 22 degrees." {
		t.Errorf("content = %q, want %q", result.Content, "The temperature is 22 degrees.")
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("tool calls = %d, want 0", len(result.ToolCalls))
	}
}
