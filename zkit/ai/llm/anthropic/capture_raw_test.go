package anthropic_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestNativeCaptureRetainsUnknownFields(t *testing.T) {
	t.Parallel()
	for _, stream := range []bool{false, true} {
		name := "complete"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				blocks := []string{`{"type":"thinking","thinking":"thought","signature":"signed","future":"private-canary"}`, `{"type":"redacted_thinking","data":"opaque","future":"private-canary"}`, `{"type":"text","text":"answer","future":"private-canary"}`}
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[` + strings.Join(blocks, ",") + `],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				writeAnthropicEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-test","usage":{"input_tokens":1,"output_tokens":0}}}`)
				for i, block := range blocks {
					index := string(rune('0' + i))
					writeAnthropicEvent(w, "content_block_start", `{"type":"content_block_start","index":`+index+`,"content_block":`+block+`}`)
					writeAnthropicEvent(w, "content_block_stop", `{"type":"content_block_stop","index":`+index+`}`)
				}
				writeAnthropicEvent(w, "message_stop", `{"type":"message_stop"}`)
			}))
			t.Cleanup(server.Close)
			provider := anthropic.NewProvider("test-key", anthropic.WithBaseURL(server.URL))
			count := 0
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: stream, Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}}) {
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range chunk.CompletedItems {
					count++
					if !strings.Contains(string(item.Data), `"future":"private-canary"`) {
						t.Errorf("unknown field discarded for %s", item.Kind)
					}
				}
			}
			if count != 3 {
				t.Fatalf("captured %d blocks", count)
			}
		})
	}
}
