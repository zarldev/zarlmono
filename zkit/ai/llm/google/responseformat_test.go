package google_test

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestProvider_ResponseFormat(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"v": map[string]any{"type": "string"}}}
	tests := []struct {
		name       string
		format     llm.ResponseFormat
		wantMIME   string
		wantSchema bool
	}{
		{"text is a no-op", llm.ResponseFormat{}, "", false},
		{"json object sets mime only", llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}, "application/json", false},
		{"json schema sets mime and schema", llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.SchemaFromMap(schema)}, "application/json", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var request map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"))
			}))
			defer server.Close()

			p, err := google.NewProvider("test-key", google.WithBaseURL(server.URL), google.WithModel("gemini-2.0-flash"))
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{
				Messages:       []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
				ResponseFormat: tc.format,
			}) {
				if err != nil {
					t.Fatalf("Complete: %v", err)
				}
			}

			config, _ := request["generationConfig"].(map[string]any)
			if got, _ := config["responseMimeType"].(string); got != tc.wantMIME {
				t.Errorf("responseMimeType = %q, want %q", got, tc.wantMIME)
			}
			_, hasSchema := config["responseJsonSchema"]
			if hasSchema != tc.wantSchema {
				t.Errorf("responseJsonSchema present = %v, want %v", hasSchema, tc.wantSchema)
			}
		})
	}
}

func TestProvider_Temperature(t *testing.T) {
	tests := []struct {
		name        string
		temperature float32
		wantSet     bool
	}{
		{"unset uses server default", 0, false},
		{"explicit temperature is forwarded", 0.7, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var request map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&request)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"))
			}))
			defer server.Close()

			p, err := google.NewProvider("test-key", google.WithBaseURL(server.URL))
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			for range p.Complete(t.Context(), llm.CompletionRequest{
				Messages:    []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
				Temperature: tc.temperature,
			}) {
			}
			config, _ := request["generationConfig"].(map[string]any)
			got, set := config["temperature"]
			if set != tc.wantSet {
				t.Fatalf("temperature present = %v, want %v", set, tc.wantSet)
			}
			if tc.wantSet {
				want := float64(tc.temperature)
				if got, ok := got.(float64); !ok || math.Abs(got-want) > 1e-6 {
					t.Errorf("temperature = %v, want %v", got, want)
				}
			}
		})
	}
}
