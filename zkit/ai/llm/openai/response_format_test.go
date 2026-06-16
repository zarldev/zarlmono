package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

// TestRequest_ResponseFormatInjection checks the wire-format
// behaviour of llm.ResponseFormat: when set, the outgoing chat
// completion body must include a response_format field with the
// OpenAI-compatible shape (which llama.cpp and vLLM also accept).
// The empty zero value must NOT inject the field — sending
// response_format:null upsets some stricter servers.
func TestRequest_ResponseFormatInjection(t *testing.T) {
	tests := []struct {
		name   string
		format llm.ResponseFormat
		check  func(t *testing.T, body map[string]any)
	}{
		{
			name:   "ZeroValue_NoResponseFormat",
			format: llm.ResponseFormat{},
			check: func(t *testing.T, body map[string]any) {
				if _, ok := body["response_format"]; ok {
					t.Errorf("response_format injected for zero-value ResponseFormat; want absent")
				}
			},
		},
		{
			name:   "JSONObject_SetsTypeOnly",
			format: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject},
			check: func(t *testing.T, body map[string]any) {
				rf, ok := body["response_format"].(map[string]any)
				if !ok {
					t.Fatalf("response_format missing or wrong type: %v", body["response_format"])
				}
				if rf["type"] != "json_object" {
					t.Errorf("type = %v, want json_object", rf["type"])
				}
				if _, present := rf["json_schema"]; present {
					t.Errorf("json_schema present for json_object mode")
				}
			},
		},
		{
			name: "JSONSchema_VerdictEnum",
			format: llm.ResponseFormat{
				Type:   llm.ResponseFormatJSONSchema,
				Name:   "verdict",
				Strict: true,
				Schema: llm.SchemaFromMap(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"verdict": map[string]any{
							"type": "string",
							"enum": []string{"improved", "unchanged", "regressed", "unclear"},
						},
					},
					"required":             []string{"verdict"},
					"additionalProperties": false,
				}),
			},
			check: func(t *testing.T, body map[string]any) {
				rf, ok := body["response_format"].(map[string]any)
				if !ok {
					t.Fatalf("response_format missing or wrong type: %v", body["response_format"])
				}
				if rf["type"] != "json_schema" {
					t.Errorf("type = %v, want json_schema", rf["type"])
				}
				inner, ok := rf["json_schema"].(map[string]any)
				if !ok {
					t.Fatalf("json_schema missing or wrong type: %v", rf["json_schema"])
				}
				if inner["name"] != "verdict" {
					t.Errorf("name = %v, want verdict", inner["name"])
				}
				if inner["strict"] != true {
					t.Errorf("strict = %v, want true", inner["strict"])
				}
				schema, ok := inner["schema"].(map[string]any)
				if !ok {
					t.Fatalf("schema missing or wrong type: %v", inner["schema"])
				}
				props, ok := schema["properties"].(map[string]any)
				if !ok {
					t.Fatalf("schema.properties missing: %v", schema)
				}
				verdict, ok := props["verdict"].(map[string]any)
				if !ok {
					t.Fatalf("schema.properties.verdict missing: %v", props)
				}
				enum, ok := verdict["enum"].([]any)
				if !ok {
					t.Fatalf("schema.properties.verdict.enum missing: %v", verdict)
				}
				if len(enum) != 4 {
					t.Errorf("enum len = %d, want 4 (%v)", len(enum), enum)
				}
			},
		},
		{
			name: "JSONSchema_EmptySchemaSkipsInjection",
			format: llm.ResponseFormat{
				Type: llm.ResponseFormatJSONSchema,
				Name: "noop",
			},
			check: func(t *testing.T, body map[string]any) {
				if _, ok := body["response_format"]; ok {
					t.Errorf("response_format injected with empty schema; want skipped")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured, _ = io.ReadAll(r.Body)
				// Minimal SSE that satisfies the streaming loop. We
				// don't care about the response — only the body the
				// SDK sent.
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(
					[]byte(
						`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"ok"},"finish_reason":"stop","index":0}]}` + "\n\n",
					),
				)
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}))
			defer srv.Close()

			provider, err := openai.NewProvider("test-key", openai.WithBaseURL(srv.URL))
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}

			req := llm.CompletionRequest{
				Messages:       []llm.Message{{Role: "user", Content: "hi"}},
				Stream:         true,
				ResponseFormat: tc.format,
			}

			seq, err := provider.Complete(context.Background(), req)
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			for range seq {
			}

			var body map[string]any
			if err := json.Unmarshal(captured, &body); err != nil {
				t.Fatalf("unmarshal request body: %v\nbody: %s", err, captured)
			}
			tc.check(t, body)
		})
	}
}
