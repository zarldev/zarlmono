package openai_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestResponsesTerminalEventClosesStillOpenConnection(t *testing.T) {
	for _, adapter := range []struct {
		name  string
		build func(string) llm.Provider
	}{
		{
			name: "openai",
			build: func(url string) llm.Provider {
				return openai.NewProvider("test-key", openai.WithBaseURL(url), openai.WithResponsesAPI(true), openai.WithModel("gpt-5.6"))
			},
		},
		{
			name: "codex",
			build: func(url string) llm.Provider {
				return openaicodex.NewProvider(
					openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "test-token", AccountID: "test-account"}},
					openaicodex.WithBaseURL(url),
					openaicodex.WithNoRetry(),
				)
			},
		},
	} {
		t.Run(adapter.name, func(t *testing.T) {
			handlerDone := make(chan struct{})
			releaseHandler := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-releaseHandler:
				}
			}))
			defer func() {
				close(releaseHandler)
				server.Close()
			}()

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var got error
			for _, err := range adapter.build(server.URL).Complete(ctx, llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
				if err != nil {
					got = err
				}
			}
			if ctx.Err() != nil {
				t.Fatal("completion waited for EOF after terminal event")
			}
			if got != nil {
				t.Fatalf("Complete: %v", got)
			}
			select {
			case <-handlerDone:
			case <-ctx.Done():
				t.Fatal("terminal completion did not close its response body")
			}
		})
	}
}
