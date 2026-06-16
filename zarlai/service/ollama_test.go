package service_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Ollama now speaks through zkit's OpenAI-compatible /v1 provider, so these
// fixtures mirror the OpenAI chat-completions wire format served at
// /v1/chat/completions rather than Ollama's native /api/chat.

func openAIChatResponse(content string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": content},
			},
		},
	}
}

func TestOllamaChat(t *testing.T) {
	tests := []struct {
		name     string
		messages []service.Message
		response string
		want     string
	}{
		{
			name:     "simple response",
			messages: []service.Message{{Role: "user", Content: "hello"}},
			response: "Hi there!",
			want:     "Hi there!",
		},
		{
			name: "multi-turn",
			messages: []service.Message{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "Hi!"},
				{Role: "user", Content: "how are you?"},
			},
			response: "I'm doing great!",
			want:     "I'm doing great!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if r.Method != http.MethodPost {
					t.Errorf("unexpected method: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(openAIChatResponse(tt.response))
			}))
			defer srv.Close()

			o := service.NewOllamaClient(srv.URL, "test-model", nil)
			result, err := o.Chat(t.Context(), tt.messages, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Content != tt.want {
				t.Errorf("got %q, want %q", result.Content, tt.want)
			}
		})
	}
}

func TestOllamaChatWithImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		// Vision rides as an OpenAI image_url content part carrying the
		// base64 data URI, not Ollama's native images[] array.
		if !strings.Contains(body, "image_url") || !strings.Contains(body, "base64data") {
			t.Errorf("expected image_url part with base64 data, got: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAIChatResponse("I see a cat."))
	}))
	defer srv.Close()

	o := service.NewOllamaClient(srv.URL, "test-model", nil)
	msgs := []service.Message{
		{Role: "user", Content: "what do you see?", Images: []string{"base64data"}},
	}
	result, err := o.Chat(t.Context(), msgs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "I see a cat." {
		t.Errorf("got %q, want %q", result.Content, "I see a cat.")
	}
}

func TestOllamaChat_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAIChatResponse("The temperature is 22 degrees."))
	}))
	defer srv.Close()

	client := service.NewOllamaClient(srv.URL, "test-model", nil)
	result, err := client.Chat(t.Context(), []service.Message{
		{Role: "user", Content: "What's the temperature?"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "The temperature is 22 degrees." {
		t.Errorf("content = %q", result.Content)
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(result.ToolCalls))
	}
}

func TestOllamaChat_ToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "test-model",
			"choices": []map[string]any{
				{
					"index":         0,
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_abc",
								"type": "function",
								"function": map[string]any{
									"name":      "get_state",
									"arguments": `{"entity_id":"sensor.temperature"}`,
								},
							},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	client := service.NewOllamaClient(srv.URL, "test-model", nil)
	result, err := client.Chat(t.Context(), []service.Message{
		{Role: "user", Content: "What's the temperature?"},
	}, []llm.Tool{
		{Type: "function", Function: llm.ToolFunction{Name: "get_state"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "get_state" {
		t.Errorf("tool name = %q, want get_state", result.ToolCalls[0].Function.Name)
	}
	entityID := service.Get[string](result.ToolCalls[0].Function.Arguments, "entity_id")
	if entityID != "sensor.temperature" {
		t.Errorf("entity_id = %q, want sensor.temperature", entityID)
	}
}
