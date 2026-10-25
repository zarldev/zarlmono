package anthropic_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestToolCacheBreakpointIsIndependentOfSystemPrompt(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		var toolPrefix []byte
		for _, system := range []string{"", "PLAN", "BUILD"} {
			req := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "inspect"}}}
			if system != "" {
				req.Messages = append([]llm.Message{{Role: llm.RoleSystem, Content: system}}, req.Messages...)
			}
			for _, name := range []string{"read", "bash", "write"}[:count] {
				req.Tools = append(req.Tools, llm.Tool{Type: "function", Function: llm.ToolFunction{Name: name, Description: "tool " + name}})
			}
			// Clone the fixture's populated slices without assigning a wire format
			// to CompletionRequest; all nested tool parameter maps are nil here.
			before := req
			before.Messages = llm.CloneMessages(req.Messages)
			before.Tools = slices.Clone(req.Tools)
			body := anthropicRequestBody(t, req)
			if !reflect.DeepEqual(before, req) {
				t.Fatal("cache conversion mutated request")
			}
			tools, _ := body["tools"].([]any)
			serialized, err := json.Marshal(tools)
			if err != nil {
				t.Fatal(err)
			}
			if toolPrefix == nil {
				toolPrefix = serialized
			} else if !bytes.Equal(toolPrefix, serialized) {
				t.Errorf("%s system prompt changed independently cached tool definitions", system)
			}
			if len(tools) != count {
				t.Fatalf("tools = %d, want %d", len(tools), count)
			}
			breakpoints := 0
			for i, raw := range tools {
				tool := raw.(map[string]any)
				cc, marked := tool["cache_control"].(map[string]any)
				if marked != (i == count-1) {
					t.Errorf("tool %d marked = %v, count=%d", i, marked, count)
				}
				if marked {
					breakpoints++
					if cc["type"] != "ephemeral" || cc["ttl"] != "1h" {
						t.Errorf("tool TTL = %v", cc)
					}
				}
			}
			if system != "" {
				cc := body["system"].([]any)[0].(map[string]any)["cache_control"].(map[string]any)
				breakpoints++
				if cc["ttl"] != "1h" {
					t.Errorf("system TTL is not equal to tools TTL: %v", cc)
				}
			}
			messages := body["messages"].([]any)
			tail := messageContent(t, messages[len(messages)-1])[0]
			cc := tail["cache_control"].(map[string]any)
			breakpoints++
			if cc["type"] != "ephemeral" || cc["ttl"] != nil || breakpoints > 3 {
				t.Errorf("tail TTL/order or breakpoint budget changed: %v, count=%d", cc, breakpoints)
			}
		}
	}
}
