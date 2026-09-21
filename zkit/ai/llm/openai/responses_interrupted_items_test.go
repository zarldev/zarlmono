package openai_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestResponsesCompletedItemsSurviveStreamTermination(t *testing.T) {
	t.Parallel()
	const reasoning = `{"type":"reasoning","id":"rs_owned","encrypted_content":"opaque","summary":[]}`
	const message = `{"type":"message","id":"msg_owned","role":"assistant","content":[{"type":"output_text","text":"answer"}]}`
	for _, adapter := range []struct {
		name  string
		build func(string) llm.Provider
	}{
		{"openai", func(url string) llm.Provider {
			return openai.NewProvider("key", openai.WithBaseURL(url), openai.WithResponsesAPI(true), openai.WithModel("gpt-5.6"))
		}},
		{"codex", func(url string) llm.Provider {
			return openaicodex.NewProvider(openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "access", AccountID: "account"}}, openaicodex.WithBaseURL(url), openaicodex.WithNoRetry())
		}},
	} {
		t.Run(adapter.name, func(t *testing.T) {
			t.Parallel()
			for _, ending := range []struct {
				name, payload string
				errors        int
				items         int
			}{
				{"failed", `data: {"type":"response.failed","response":{"error":{"message":"interrupted"}}}` + "\n\n", 1, 2},
				{"scanner", "data: " + strings.Repeat("x", 4*1024*1024) + "\n\n", 1, 2},
				{"eof", "", 0, 2},
				{"completed", `data: {"type":"response.completed","response":{"output":[` + reasoning + "," + message + "," + reasoning + `]}}` + "\n\n", 0, 3},
			} {
				t.Run(ending.name, func(t *testing.T) {
					t.Parallel()
					payload := `data: {"type":"response.output_item.done","output_index":0,"item":` + reasoning + "}\n\n" +
						`data: {"type":"response.output_item.done","output_index":1,"item":` + message + "}\n\n" + ending.payload
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, payload)
					}))
					t.Cleanup(server.Close)
					var items []llm.ContinuationItem
					errors := 0
					for chunk, err := range adapter.build(server.URL).Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}}}) {
						if errors != 0 {
							t.Fatal("yield after terminal error")
						}
						if err != nil {
							errors++
							if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
								t.Fatalf("nonzero terminal chunk: %+v", chunk)
							}
							continue
						}
						items = append(items, chunk.Clone().CompletedItems...)
					}
					if errors != ending.errors || len(items) != ending.items {
						t.Fatalf("errors=%d items=%d; want %d, %d", errors, len(items), ending.errors, ending.items)
					}
					for i, item := range items {
						want := reasoning
						if i == 1 {
							want = message
						}
						if item.OutputIndex == nil || *item.OutputIndex != i || string(item.Data) != want {
							t.Fatalf("item %d = %+v data=%s", i, item, item.Data)
						}
					}
				})
			}
		})
	}
}
