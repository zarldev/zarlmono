package anthropic_test

import (
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestToolCacheBreakpointIndependentOfSystemPrompt(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		var prefix string
		for _, system := range []string{"", "PLAN", "BUILD"} {
			req := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "inspect"}}}
			if system != "" {
				req.Messages = append([]llm.Message{{Role: llm.RoleSystem, Content: system}}, req.Messages...)
			}
			for _, name := range []string{"read", "bash", "write"}[:count] {
				req.Tools = append(req.Tools, llm.Tool{Type: "function", Function: llm.ToolFunction{Name: name, Description: "tool " + name}})
			}
			body := anthropicRequestBody(t, req)
			tools, _ := body["tools"].([]any)
			serialized, err := json.Marshal(tools)
			if err != nil {
				t.Fatal(err)
			}
			if prefix == "" {
				prefix = string(serialized)
			} else if prefix != string(serialized) {
				t.Errorf("system prompt changed tool cache prefix")
			}
			if len(tools) != count {
				t.Fatalf("tools=%d, want %d", len(tools), count)
			}
			for i, raw := range tools {
				cc, marked := raw.(map[string]any)["cache_control"].(map[string]any)
				if marked != (i == count-1) {
					t.Errorf("tool %d marked=%v", i, marked)
				}
				if marked && (cc["type"] != "ephemeral" || cc["ttl"] != "1h") {
					t.Errorf("tool cache=%v", cc)
				}
			}
		}
	}
}
