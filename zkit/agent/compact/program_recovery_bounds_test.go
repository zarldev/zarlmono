package compact_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestTieredProgramRecoveryRecipeIsBoundedReferenceText(t *testing.T) {
	const recipe = `Read with program_output_read(receipt="not-issued", offset=0).`
	for _, tc := range []struct {
		name, suffix string
		preserved    bool
	}{
		{name: "ordinary", suffix: "ordinary result"},
		{name: "bounded recipe", suffix: recipe, preserved: true},
		{name: "maximum recipe suffix", suffix: recipe + strings.Repeat("x", 2048-len(recipe)), preserved: true},
		{name: "oversized recipe suffix", suffix: recipe + strings.Repeat("x", 2049-len(recipe))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "result", Function: llm.ToolCallFunction{Name: "read", Arguments: `{}`}}}},
				{Role: llm.RoleTool, ToolCallID: "result", Content: strings.Repeat("body", 2000) + tc.suffix},
				{Role: llm.RoleUser, Content: strings.Repeat("protected", 1000)},
			}
			compactor := compact.Tiered{TargetBytes: 1000}
			result, err := compactor.Compact(t.Context(), history, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Warning, "phase 3:") {
				t.Fatalf("phase: %s", result.Warning)
			}
			text := result.History[1].Content
			if strings.Contains(text, recipe) != tc.preserved {
				t.Fatalf("unexpected recipe preservation: %s", text)
			}
			if tc.preserved && !strings.HasSuffix(text, tc.suffix) {
				t.Fatal("bounded reference suffix changed")
			}
			if result.BytesTrimmed != compactor.WouldReduceBytes(history, 1) {
				t.Fatal("probe disagrees with bounded footer")
			}
			store := program.NewOutputStore()
			t.Cleanup(store.Close)
			if _, err := store.Read(t.Context(), program.OutputReadArgs{Receipt: "not-issued"}); err == nil {
				t.Fatal("reference text granted receipt authority")
			}
		})
	}
}
