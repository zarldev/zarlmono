package compact_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
)

func TestTieredRetainedToolCallRemainsValidDeepSeekHistory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		phase3 float64
	}{
		{name: "phase 2", phase3: 100},
		{name: "phase 3", phase3: .5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const reasoning = "provider-required tool reasoning"
			history := []llm.Message{
				{Role: llm.RoleUser, Content: "inspect the repository"},
				{
					Role: llm.RoleAssistant, Content: strings.Repeat("analysis ", 200), ReasoningContent: reasoning,
					ToolCalls: []llm.ToolCall{{ID: "call-1", Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"main.go"}`}}},
				},
				{Role: llm.RoleTool, ToolCallID: "call-1", Content: strings.Repeat("result ", 100)},
				{Role: llm.RoleUser, Content: "continue"},
			}
			compactor := compact.Tiered{TargetBytes: 1000, Phase1Threshold: .1, Phase2Threshold: .2, Phase3Threshold: tc.phase3}
			probe := compactor.WouldReduceBytes(history, 1)
			result, err := compactor.Compact(t.Context(), history, 1)
			if err != nil {
				t.Fatalf("Compact: %v", err)
			}
			if !strings.Contains(result.Warning, tc.name) {
				t.Fatalf("warning = %q, want %s", result.Warning, tc.name)
			}

			actual := retainedHistoryBytes(history) - retainedHistoryBytes(result.History)
			if probe != result.BytesTrimmed || probe != actual {
				t.Fatalf("probe=%d reported=%d actual=%d", probe, result.BytesTrimmed, actual)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []struct {
						ReasoningContent string         `json:"reasoning_content"`
						ToolCalls        []llm.ToolCall `json:"tool_calls"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				for _, message := range request.Messages {
					if len(message.ToolCalls) > 0 && message.ReasoningContent == reasoning {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte("data: [DONE]\n\n"))
						return
					}
				}
				http.Error(w, "missing reasoning_content for retained tool call", http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)

			provider := deepseek.NewProvider("test-key", deepseek.WithBaseURL(server.URL), deepseek.WithModel("deepseek-v4"))
			for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: result.History, Stream: true}) {
				if err != nil {
					t.Fatalf("DeepSeek rejected compacted history: %v", err)
				}
			}
		})
	}
}

func TestTieredPlainActionReasoningDoesNotInventSavings(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		{Role: llm.RoleAssistant, Content: "short", ReasoningContent: strings.Repeat("required reasoning ", 300), ToolCalls: []llm.ToolCall{{ID: "call", Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: "{}"}}}},
		{Role: llm.RoleTool, ToolCallID: "call", Content: "ok"},
		{Role: llm.RoleUser, Content: "continue"},
	}
	compactor := compact.Tiered{TargetBytes: 1000, Phase1Threshold: .1, Phase2Threshold: .2, Phase3Threshold: .3}
	probe := compactor.WouldReduceBytes(history, 1)
	result, err := compactor.Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatal(err)
	}
	actual := retainedHistoryBytes(history) - retainedHistoryBytes(result.History)
	if probe != 0 || result.BytesTrimmed != 0 || actual != 0 {
		t.Fatalf("no-op reasoning reported relief: probe=%d reported=%d actual=%d", probe, result.BytesTrimmed, actual)
	}
}
