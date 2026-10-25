package compact_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestKeepRecentExchange(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "request"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: llm.RoleTool, ToolCallID: "a"},
		{Role: llm.RoleTool, ToolCallID: "b"},
		{Role: llm.RoleUser, Content: "new steering"},
	}
	for _, tc := range []struct{ keep, want int }{{0, 0}, {1, 4}, {2, 4}, {3, 4}, {5, 5}} {
		if got := compact.KeepRecentExchange(history, tc.keep); got != tc.want {
			t.Errorf("keep %d = %d, want %d", tc.keep, got, tc.want)
		}
	}
	history = append(history, llm.Message{Role: llm.RoleAssistant, Content: "consumed"})
	if got := compact.KeepRecentExchange(history, 1); got != 1 {
		t.Errorf("completed exchange remains protected: %d", got)
	}
}

func TestTieredRetainsRecoveryFooterAcrossPhases(t *testing.T) {
	footer := "\n\n[truncated by bytes: 100KB / 100 lines → 2KB / 10 lines (kept tail); full output: /tmp/retained-output.log — bash can grep/head it]"
	body := "source identity\n" + strings.Repeat("middle", 2000) + "verification outcome" + footer
	for _, phase3 := range []float64{100, .9} {
		c := compact.Tiered{TargetBytes: 100, Phase3Threshold: phase3}
		history := []llm.Message{{Role: llm.RoleTool, ToolCallID: "a", Content: body}, {Role: llm.RoleUser, Content: "recent"}}
		result, err := c.Compact(t.Context(), history, 1)
		if err != nil {
			t.Fatal(err)
		}
		got := result.History[0].Content
		if !strings.HasPrefix(got, "source identity") || !strings.Contains(got, footer) {
			t.Fatalf("lost source or recovery footer: %s", got)
		}
		if result.BytesTrimmed != len(body)-len(got) || c.WouldReduceBytes(history, 1) != result.BytesTrimmed {
			t.Fatalf("incorrect savings: %+v", result)
		}
	}
}
