package openai_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestUsagePresenceDistinguishesAbsentNullAndZero(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []struct {
		name      string
		responses bool
		event     func(string) string
	}{
		{
			name: "chat completions",
			event: func(usage string) string {
				return `{"id":"x","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop","index":0}]` + usage + `}`
			},
		},
		{
			name:      "responses",
			responses: true,
			event: func(usage string) string {
				return `{"type":"response.completed","response":{"status":"completed"` + usage + `}}`
			},
		},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name     string
				usage    string
				reported bool
			}{
				{name: "absent"},
				{name: "null", usage: `,"usage":null`},
				{name: "reported zero", usage: `,"usage":{}`, reported: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "data: %s\n\n", endpoint.event(tc.usage))
						if !endpoint.responses {
							_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
						}
					}))
					t.Cleanup(server.Close)
					provider := openai.NewProvider("test-key", openai.WithBaseURL(server.URL), openai.WithModel("gpt-5.6"), openai.WithResponsesAPI(endpoint.responses))
					var last llm.CompletionChunk
					for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
						if err != nil {
							t.Fatalf("Complete: %v", err)
						}
						last = chunk
					}
					if last.UsageReported != tc.reported {
						t.Fatalf("UsageReported = %v, want %v; chunk=%+v", last.UsageReported, tc.reported, last)
					}
					if last.Usage != (llm.Usage{}) {
						t.Fatalf("usage = %+v, want zero", last.Usage)
					}
				})
			}
		})
	}
}
