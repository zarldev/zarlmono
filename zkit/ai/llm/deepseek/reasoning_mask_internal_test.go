package deepseek

import (
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestKeepReasoningMask(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msgs []llm.Message
		want []bool
	}{
		{
			name: "lone answer turn is tool-free",
			msgs: []llm.Message{{Role: llm.RoleUser}, {Role: llm.RoleAssistant}},
			want: []bool{false, false},
		},
		{
			// Delimiter user messages are never inside a window, so their
			// mask value is a don't-care left false; only the assistant
			// turns (indices 1 and 3 here) are load-bearing.
			name: "tool-call window keeps the whole window",
			msgs: []llm.Message{
				{Role: llm.RoleUser},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1"}}},
				{Role: llm.RoleTool},
				{Role: llm.RoleAssistant},
			},
			want: []bool{false, true, true, true},
		},
		{
			name: "tool-free window after a tool-call window drops only itself",
			msgs: []llm.Message{
				{Role: llm.RoleUser},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1"}}},
				{Role: llm.RoleTool},
				{Role: llm.RoleAssistant},
				{Role: llm.RoleUser},
				{Role: llm.RoleAssistant},
			},
			want: []bool{false, true, true, true, false, false},
		},
		{
			name: "trailing open tool-call window is kept",
			msgs: []llm.Message{
				{Role: llm.RoleUser},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1"}}},
				{Role: llm.RoleTool},
			},
			want: []bool{false, true, true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := keepReasoningMask(c.msgs); !slices.Equal(got, c.want) {
				t.Errorf("keepReasoningMask = %v, want %v", got, c.want)
			}
		})
	}
}
