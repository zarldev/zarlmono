package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAnthropicChat_ToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Model != "claude-3-5-sonnet-20241022" {
			t.Errorf("model = %q, want claude-3-5-sonnet-20241022", body.Model)
		}
		if len(body.Tools) != 1 {
			t.Errorf("tools count = %d, want 1", len(body.Tools))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":            "msg_test",
			"type":          "message",
			"role":          "assistant",
			"model":         "claude-3-5-sonnet-20241022",
			"stop_reason":   "tool_use",
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 20,
			},
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"id":    "toolu_get_state_0",
					"name":  "get_state",
					"input": map[string]any{"entity_id": "sensor.temperature"},
				},
			},
		})
	}))
	defer srv.Close()

	client := service.NewAnthropicClient(
		"test-key",
		"claude-3-5-sonnet-20241022",
		service.WithAnthropicBaseURL(srv.URL),
	)
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

func TestAnthropicChat_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":            "msg_test",
			"type":          "message",
			"role":          "assistant",
			"model":         "claude-3-5-haiku-20241022",
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 10,
			},
			"content": []map[string]any{
				{
					"type": "text",
					"text": "The temperature is 22 degrees.",
				},
			},
		})
	}))
	defer srv.Close()

	client := service.NewAnthropicClient(
		"test-key",
		"claude-3-5-haiku-20241022",
		service.WithAnthropicBaseURL(srv.URL),
	)
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
