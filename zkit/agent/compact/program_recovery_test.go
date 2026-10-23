package compact_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestTieredProgramRecoveryAcrossPhases(t *testing.T) {
	for _, tc := range []struct{ phase, target int }{{1, 12000}, {2, 8000}, {3, 1000}} {
		t.Run(fmt.Sprintf("phase%d", tc.phase), func(t *testing.T) {
			history, store, want := programRecoveryHistory(t)
			original := llm.CloneMessages(history)
			compactor := compact.Tiered{TargetBytes: tc.target}
			result, err := compactor.Compact(t.Context(), history, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Warning, fmt.Sprintf("phase %d:", tc.phase)) {
				t.Fatalf("wrong phase: %s", result.Warning)
			}
			if !reflect.DeepEqual(history, original) || !reflect.DeepEqual(result.History[len(result.History)-1], original[len(original)-1]) {
				t.Fatal("input or protected recent content changed")
			}
			if result.BytesTrimmed <= 0 || result.BytesTrimmed != compactor.WouldReduceBytes(history, 1) || result.BytesTrimmed != retainedHistoryBytes(history)-retainedHistoryBytes(result.History) {
				t.Fatal("incorrect savings accounting")
			}
			if _, repaired := compact.RepairToolPairing(result.History); repaired != 0 {
				t.Fatal("compaction broke tool pairing")
			}
			var overflow string
			for _, msg := range result.History {
				if msg.ToolCallID == "overflow" {
					overflow = msg.Content
				}
			}
			receipt := compactedProgramReceipt(t, overflow)
			reader := program.NewOutputReadTool(store)
			var recovered strings.Builder
			for offset := 0; ; {
				pageResult, err := reader.Execute(t.Context(), tools.ToolCall{ID: "page", Arguments: tools.ToolParameters{"receipt": receipt, "offset": offset}})
				if err != nil || !pageResult.Success || len(pageResult.AdmissionReferences) != 0 {
					t.Fatalf("page: %+v, %v", pageResult, err)
				}
				page := pageResult.Data.(program.OutputPage)
				recovered.WriteString(page.Content)
				if page.Done {
					break
				}
				if page.NextOffset <= offset {
					t.Fatal("reader made no progress")
				}
				offset = page.NextOffset
			}
			if !bytes.Equal([]byte(recovered.String()), want) {
				t.Fatal("retained middle/tail facts or exact JSON changed")
			}
			wireBefore, wireAfter := compactFixtureJSON(t, history), compactFixtureJSON(t, result.History)
			if len(wireAfter) >= len(wireBefore) {
				t.Fatal("serialized message array did not shrink")
			}
			// Tiered is explicitly lossy outside the protected suffix and retained
			// output. Do not claim a middle assistant fact survives phases 2/3.
			if bytes.Contains(wireAfter, []byte("ASSISTANT_MIDDLE")) != (tc.phase == 1) {
				t.Fatal("unexpected assistant middle-fact projection")
			}
			if !bytes.Contains(wireAfter, []byte("ASSISTANT_TAIL")) {
				t.Fatal("assistant tail fact lost")
			}
			again, err := compactor.Compact(t.Context(), result.History, 1)
			if err != nil {
				t.Fatal(err)
			}
			// Ordinary elision annotations may shrink again on a later pass;
			// the recovery recipe itself must remain exact and discoverable.
			for _, msg := range again.History {
				if msg.ToolCallID == "overflow" && msg.Content != overflow {
					t.Fatal("repeated compaction changed recovery recipe")
				}
			}
			if again.BytesTrimmed != compactor.WouldReduceBytes(result.History, 1) {
				t.Fatal("repeated compaction probe drift")
			}
			t.Logf("message-array JSON bytes: %d -> %d (%.1f%% reduction); retained-field savings=%d; recovered JSON=%d", len(wireBefore), len(wireAfter), 100*float64(len(wireBefore)-len(wireAfter))/float64(len(wireBefore)), result.BytesTrimmed, len(want))
		})
	}
}

func programRecoveryHistory(tb testing.TB) ([]llm.Message, *program.OutputStore, []byte) {
	tb.Helper()
	store := program.NewOutputStore()
	tb.Cleanup(store.Close)
	source, err := program.NewSource(tools.NewRegistry(), program.WithOutputRetainer(store))
	if err != nil {
		tb.Fatal(err)
	}
	script := `emit("PAYLOAD_HEAD" + "x" * 35000 + "PAYLOAD_MIDDLE" + "y" * 35000 + "PAYLOAD_TAIL")`
	result, err := source.Execute(tb.Context(), tools.ToolCall{ID: "overflow", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": script}})
	if err != nil || result.Success || len(result.AdmissionReferences) != 0 {
		tb.Fatalf("overflow: %+v, %v", result, err)
	}
	payload := "PAYLOAD_HEAD" + strings.Repeat("x", 35000) + "PAYLOAD_MIDDLE" + strings.Repeat("y", 35000) + "PAYLOAD_TAIL"
	want := compactFixtureJSON(tb, payload)
	args := compactFixtureJSON(tb, map[string]string{"script": script})
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "fixture policy"},
		{Role: llm.RoleUser, Content: "preserve the recovery recipe"},
		{Role: llm.RoleAssistant, Content: "ASSISTANT_HEAD" + strings.Repeat("a", 2000) + "ASSISTANT_MIDDLE" + strings.Repeat("b", 2000) + "ASSISTANT_TAIL", ToolCalls: []llm.ToolCall{
			{ID: "overflow", Type: "function", Function: llm.ToolCallFunction{Name: "program", Arguments: string(args)}},
			{ID: "ordinary", Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"fixture"}`}},
		}},
		{Role: llm.RoleTool, ToolCallID: "overflow", Content: result.Error},
		{Role: llm.RoleTool, ToolCallID: "ordinary", Content: "TOOL_HEAD" + strings.Repeat("c", 4000) + "TOOL_MIDDLE" + strings.Repeat("d", 4000) + "TOOL_TAIL"},
		{Role: llm.RoleUser, Content: "RECENT_FACT" + strings.Repeat("p", 4000)},
	}
	return history, store, want
}

func compactedProgramReceipt(tb testing.TB, text string) string {
	tb.Helper()
	_, tail, ok := strings.Cut(text, "program_output_read(receipt=")
	if !ok {
		tb.Fatalf("compaction lost reader recipe: %s", text)
	}
	quoted, _, ok := strings.Cut(tail, ", offset=")
	if !ok {
		tb.Fatalf("compaction damaged reader recipe: %s", text)
	}
	receipt, err := strconv.Unquote(quoted)
	if err != nil {
		tb.Fatal(err)
	}
	return receipt
}

func compactFixtureJSON(tb testing.TB, value any) []byte {
	tb.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

func BenchmarkTieredProgramRecovery(b *testing.B) {
	for _, read := range []bool{false, true} {
		b.Run(fmt.Sprintf("read-page=%v", read), func(b *testing.B) {
			history, store, _ := programRecoveryHistory(b)
			compactor := compact.Tiered{TargetBytes: 1000}
			result, err := compactor.Compact(b.Context(), history, 1)
			if err != nil {
				b.Fatal(err)
			}
			var receipt string
			for _, msg := range result.History {
				if msg.ToolCallID == "overflow" {
					receipt = compactedProgramReceipt(b, msg.Content)
				}
			}
			before, after := len(compactFixtureJSON(b, history)), len(compactFixtureJSON(b, result.History))
			reader := program.NewOutputReadTool(store)
			call := tools.ToolCall{ID: "page", Arguments: tools.ToolParameters{"receipt": receipt}}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := compactor.Compact(b.Context(), history, 1); err != nil {
					b.Fatal(err)
				}
				if read {
					if result, err := reader.Execute(b.Context(), call); err != nil || !result.Success {
						b.Fatalf("read: %+v, %v", result, err)
					}
				}
			}
			b.ReportMetric(float64(before), "input-json-bytes")
			b.ReportMetric(float64(after), "compacted-json-bytes")
		})
	}
}
