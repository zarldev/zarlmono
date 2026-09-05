package compact_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestStructuralPreservesOpaqueItemsWithoutTruncationAndOwnsCopies(t *testing.T) {
	payload := make([]byte, 2048)
	for i := range payload {
		payload[i] = byte(i)
	}
	history := []llm.Message{
		{Role: llm.RoleAssistant, Content: "short", ContinuationItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: payload}}},
		{Role: llm.RoleUser, Content: "recent"},
	}

	result, err := compact.NewStructural().Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := result.History[0].ContinuationItems[0].Data
	if len(got) != len(payload) {
		t.Fatalf("opaque payload len = %d, want %d", len(got), len(payload))
	}
	payload[0] = 99
	if got[0] == 99 {
		t.Fatal("compacted history aliases opaque input bytes")
	}
}

func TestTieredPhase3DropsWholeOldOpaqueItems(t *testing.T) {
	payload := make([]byte, 1200)
	history := []llm.Message{
		{Role: llm.RoleAssistant, Content: "projected reasoning", ToolCalls: []llm.ToolCall{{ID: "call-1", Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{}`}}}, ContinuationItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: payload}}},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: "result"},
		{Role: llm.RoleUser, Content: "recent", ContinuationItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: []byte("recent-native")}}},
	}
	tiered := &compact.Tiered{TargetBytes: 1000, Phase1Threshold: .60, Phase2Threshold: .75, Phase3Threshold: .90}

	if got := tiered.WouldReduceBytes(history, 1); got <= 0 {
		t.Fatalf("WouldReduceBytes = %d, want positive from opaque bytes", got)
	}
	result, err := tiered.Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.History[0].ContinuationItems) != 0 {
		t.Fatalf("old continuation items retained: %#v", result.History[0].ContinuationItems)
	}
	if result.History[0].Content != "projected reasoning" || len(result.History[0].ToolCalls) != 1 {
		t.Fatalf("projected assistant history lost: %#v", result.History[0])
	}
	if result.History[1].ToolCallID != "call-1" {
		t.Fatalf("tool result pairing lost: %#v", result.History[1])
	}
	if got := string(result.History[2].ContinuationItems[0].Data); got != "recent-native" {
		t.Fatalf("recent continuation item = %q", got)
	}
	if result.BytesTrimmed < 1000 {
		t.Fatalf("BytesTrimmed = %d, want dominant native payload reduction", result.BytesTrimmed)
	}
}
