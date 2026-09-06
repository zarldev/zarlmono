package compact_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// --- tiered ---

// tinyBudgetTiered returns a Tiered configured with a small target
// so we can trigger phases without producing megabytes of fixture
// content. Thresholds: P1 at 600 bytes, P2 at 750, P3 at 900.
func tinyBudgetTiered() *compact.Tiered {
	return &compact.Tiered{
		TargetBytes:     1000,
		Phase1Threshold: 0.60,
		Phase2Threshold: 0.75,
		Phase3Threshold: 0.90,
	}
}

func TestTiered_BelowPhase1ThresholdIsNoOp(t *testing.T) {
	t.Parallel()
	c := tinyBudgetTiered()
	in := []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "small request"},
		{Role: "assistant", Content: "small reply"},
	}
	res, err := c.Compact(t.Context(), in, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Engine != "tiered" {
		t.Errorf("Engine = %q, want tiered", res.Engine)
	}
	if res.BytesTrimmed != 0 {
		t.Errorf("BytesTrimmed = %d, want 0 below threshold", res.BytesTrimmed)
	}
	if len(res.History) != len(in) {
		t.Errorf("len = %d, want %d", len(res.History), len(in))
	}
	if got := res.History[2].Content; got != "small reply" {
		t.Errorf("assistant content modified: %q", got)
	}
}

func TestTiered_Phase1TrimsToolResults(t *testing.T) {
	t.Parallel()
	c := tinyBudgetTiered()
	// Total bytes ~700: triggers Phase 1 (>= 600), stays below Phase 2 (750).
	in := []llm.Message{
		{Role: "system", Content: "sys"},                                    // 3
		{Role: "user", Content: "do the thing"},                             // 12
		{Role: "tool", ToolCallID: "t1", Content: strings.Repeat("a", 650)}, // 650
		{Role: "assistant", Content: "ok done"},                             // 7
	}
	res, err := c.Compact(t.Context(), in, 1) // keep last 1 (assistant)
	if err != nil {
		t.Fatal(err)
	}
	if res.BytesTrimmed == 0 {
		t.Fatalf("expected trim, got BytesTrimmed=0")
	}
	if got := res.History[2].Content; len(got) >= 650 {
		t.Errorf("tool body not trimmed: len=%d", len(got))
	}
	if res.History[2].ToolCallID != "t1" {
		t.Errorf("ToolCallID dropped: %q", res.History[2].ToolCallID)
	}
	if !strings.Contains(res.Warning, "phase 1") {
		t.Errorf("Warning should say phase 1: %q", res.Warning)
	}
	if got := res.History[3].Content; got != "ok done" {
		t.Errorf("assistant content modified in phase 1: %q", got)
	}
}

func TestTiered_Phase1ElidesOldToolAttachments(t *testing.T) {
	t.Parallel()
	oldDataURI := "data:image/png;base64," + strings.Repeat("a", 700)
	recentDataURI := "data:image/png;base64," + strings.Repeat("b", 700)
	c := &compact.Tiered{
		TargetBytes:     2000,
		Phase1Threshold: 0.60,
		Phase2Threshold: 0.75,
		Phase3Threshold: 0.90,
	}
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old"}}},
		{Role: llm.RoleTool, ToolCallID: "old", Content: "old metadata", Parts: []llm.ContentPart{llm.ImagePartFromDataURI(oldDataURI, "image/png")}},
		{Role: llm.RoleTool, ToolCallID: "recent", Content: "recent metadata", Parts: []llm.ContentPart{llm.ImagePartFromDataURI(recentDataURI, "image/png")}},
	}
	if got := c.WouldReduceBytes(history, 1); got <= 0 {
		t.Fatalf("WouldReduceBytes = %d, want media payload to trigger compaction", got)
	}
	res, err := c.Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !strings.Contains(res.Warning, "phase 1") {
		t.Fatalf("warning = %q, want phase 1", res.Warning)
	}
	if res.BytesTrimmed <= 0 {
		t.Fatalf("BytesTrimmed = %d, want attachment savings", res.BytesTrimmed)
	}
	if len(res.History[2].Parts) != 0 || !strings.Contains(res.History[2].Content, "tool attachments elided") {
		t.Fatalf("old attachment was not visibly elided: %+v", res.History[2])
	}
	if got := res.History[3].Parts[0].Image.DataURI; got != recentDataURI {
		t.Fatalf("recent attachment = %q, want preserved data URI", got)
	}
	if got := history[2].Parts[0].Image.DataURI; got != oldDataURI {
		t.Fatal("compaction mutated input history")
	}
}

func TestTiered_Phase1RetainsTinyAttachment(t *testing.T) {
	t.Parallel()
	compactor := &compact.Tiered{
		TargetBytes:     1000,
		Phase1Threshold: 0.10,
		Phase2Threshold: 0.90,
		Phase3Threshold: 0.95,
	}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("u", 100)},
		{Role: llm.RoleTool, ToolCallID: "tiny", Content: "metadata", Parts: []llm.ContentPart{llm.TextPart("x")}},
	}
	before := retainedHistoryBytes(history)
	if got := compactor.WouldReduceBytes(history, 0); got != 0 {
		t.Fatalf("WouldReduceBytes = %d, want 0 for unprofitable attachment", got)
	}
	result, err := compactor.Compact(t.Context(), history, 0)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	after := retainedHistoryBytes(result.History)
	if !strings.Contains(result.Warning, "phase 1") {
		t.Fatalf("warning = %q, want phase 1", result.Warning)
	}
	if result.BytesTrimmed != 0 || after != before {
		t.Fatalf("tiny attachment compaction: before=%d after=%d trimmed=%d", before, after, result.BytesTrimmed)
	}
	if len(result.History[1].Parts) != 1 || result.History[1].Parts[0].Text != "x" {
		t.Fatalf("tiny attachment changed: %+v", result.History[1].Parts)
	}
}

func TestTiered_Phase2TrimsAssistantContent(t *testing.T) {
	t.Parallel()
	c := tinyBudgetTiered()
	// One huge assistant message + huge tool result. Phase 1 trims
	// the tool result (650 -> ~290) but the assistant stays at 600
	// chars, so total stays >= Phase 2 trigger (750).
	in := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "do thing"},
		{Role: "assistant", Content: strings.Repeat("r", 600)},              // 600 reasoning
		{Role: "tool", ToolCallID: "t1", Content: strings.Repeat("a", 650)}, // 650
		{Role: "user", Content: "next"},
	}
	res, err := c.Compact(t.Context(), in, 1) // keep last 1 (user)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Warning, "phase 2") {
		t.Errorf("Warning should reach phase 2: %q", res.Warning)
	}
	if got := res.History[2].Content; len(got) >= 600 {
		t.Errorf("assistant content not trimmed in phase 2: len=%d", len(got))
	}
	if got := res.History[3].Content; len(got) >= 650 {
		t.Errorf("tool body not trimmed in phase 2: len=%d", len(got))
	}
}

func TestTiered_Phase3PreservesOperationalStateAndToolPairs(t *testing.T) {
	t.Parallel()
	c := tinyBudgetTiered()
	// Force phase 3: huge tool result, huge assistant. Even after
	// phase 2 trims the assistant to ~256 chars, the structure plus
	// the tool's truncated content still exceeds 900 bytes... or
	// does it? Need to size carefully.
	in := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "do thing"},
		{Role: "assistant", Content: strings.Repeat("r", 2000), ToolCalls: []llm.ToolCall{{
			ID: "t1", Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"first.go"}`},
		}}},
		{Role: "tool", ToolCallID: "t1", Content: strings.Repeat("a", 2000)},
		{Role: "assistant", Content: strings.Repeat("s", 2000), ToolCalls: []llm.ToolCall{{
			ID: "t2", Type: "function", Function: llm.ToolCallFunction{Name: "grep", Arguments: `{"pattern":"promise"}`},
		}}},
		{Role: "tool", ToolCallID: "t2", Content: strings.Repeat("b", 2000)},
		{Role: "user", Content: "next"},
	}
	res, err := c.Compact(t.Context(), in, 1) // keep last 1 (user)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Warning, "phase 3") {
		t.Errorf("expected phase 3 trigger, got: %q", res.Warning)
	}
	// Phase 3 must retain bounded assistant operational-state capsules.
	if got := res.History[2].Content; !strings.HasSuffix(got, strings.Repeat("r", 128)) {
		t.Errorf("phase 3: first assistant operational tail lost: %q", got)
	}
	if got := res.History[4].Content; !strings.HasSuffix(got, strings.Repeat("s", 128)) {
		t.Errorf("phase 3: second assistant operational tail lost: %q", got)
	}
	// Tool messages should be replaced with placeholders, NOT dropped.
	if got := res.History[3].Content; !strings.Contains(got, "[tool result elided") {
		t.Errorf("phase 3: tool body not collapsed: %q", got[:min(50, len(got))])
	}
	// ToolCallID linkage preserved.
	if res.History[3].ToolCallID != "t1" || res.History[5].ToolCallID != "t2" {
		t.Errorf("tool call ids dropped in phase 3")
	}
	declared := map[string]bool{}
	for _, msg := range res.History {
		for _, call := range msg.ToolCalls {
			declared[call.ID] = true
		}
		if msg.Role == llm.RoleTool && !declared[msg.ToolCallID] {
			t.Errorf("phase 3 produced orphan tool result %q", msg.ToolCallID)
		}
	}
}

func TestTiered_LaterPhasesNeverGrowHistory(t *testing.T) {
	t.Parallel()
	compactor := tinyBudgetTiered()
	assistantContent := strings.Repeat("a", 257)
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleTool, ToolCallID: "tiny", Content: "ok", Parts: []llm.ContentPart{llm.TextPart("x")}},
		{Role: llm.RoleAssistant, Content: assistantContent},
		{Role: llm.RoleUser, Content: strings.Repeat("pressure", 200)},
	}
	before := retainedHistoryBytes(history)
	if got := compactor.WouldReduceBytes(history, 1); got != 0 {
		t.Fatalf("WouldReduceBytes = %d, want 0 when selected phases cannot shrink history", got)
	}
	result, err := compactor.Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	after := retainedHistoryBytes(result.History)
	if !strings.Contains(result.Warning, "phase 3") {
		t.Fatalf("warning = %q, want phase 3", result.Warning)
	}
	if result.BytesTrimmed < 0 || after > before {
		t.Fatalf("compaction grew history: before=%d after=%d trimmed=%d", before, after, result.BytesTrimmed)
	}
	if result.BytesTrimmed != before-after {
		t.Fatalf("BytesTrimmed = %d, want %d", result.BytesTrimmed, before-after)
	}
	if result.History[1].Content != "ok" || len(result.History[1].Parts) != 1 {
		t.Fatalf("unprofitable tool replacements changed message: %+v", result.History[1])
	}
	if result.History[2].Content != assistantContent {
		t.Fatal("unprofitable assistant replacement changed content")
	}
}

func TestTiered_ProbeMatchesSelectedPhaseSavings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		history     []llm.Message
		compactor   *compact.Tiered
		wantPhase   string
		wantSavings bool
	}{
		{
			name: "exact phase 1 threshold",
			history: []llm.Message{{
				Role: llm.RoleTool, ToolCallID: "image",
				Parts: []llm.ContentPart{llm.ImagePartFromDataURI("data:image/png;base64,"+strings.Repeat("a", 256), "image/png")},
			}},
			wantPhase: "phase 1", wantSavings: true,
		},
		{
			name:      "phase 2 assistant reduction",
			history:   []llm.Message{{Role: llm.RoleAssistant, Content: strings.Repeat("a", 800)}},
			compactor: &compact.Tiered{TargetBytes: 1000, Phase1Threshold: 0.10, Phase2Threshold: 0.20, Phase3Threshold: 0.90},
			wantPhase: "phase 2", wantSavings: true,
		},
		{
			name:      "user-only pressure",
			history:   []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("u", 1000)}},
			compactor: tinyBudgetTiered(),
			wantPhase: "phase 3",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			compactor := tc.compactor
			if compactor == nil {
				thresholdBytes := retainedHistoryBytes(tc.history)
				compactor = &compact.Tiered{TargetBytes: thresholdBytes, Phase1Threshold: 1, Phase2Threshold: 2, Phase3Threshold: 3}
			}
			probe := compactor.WouldReduceBytes(tc.history, 0)
			result, err := compactor.Compact(t.Context(), tc.history, 0)
			if err != nil {
				t.Fatalf("Compact: %v", err)
			}
			if !strings.Contains(result.Warning, tc.wantPhase) {
				t.Fatalf("warning = %q, want %s", result.Warning, tc.wantPhase)
			}
			if tc.wantSavings && probe <= 0 {
				t.Fatalf("WouldReduceBytes = %d, want positive", probe)
			}
			if !tc.wantSavings && probe != 0 {
				t.Fatalf("WouldReduceBytes = %d, want 0", probe)
			}
			if probe != result.BytesTrimmed {
				t.Fatalf("WouldReduceBytes = %d, BytesTrimmed = %d", probe, result.BytesTrimmed)
			}
		})
	}
}

func TestTiered_WouldReduceBytesDoesNotAllocate(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleTool, Content: strings.Repeat("tool", 200), Parts: []llm.ContentPart{
			llm.ImagePartFromDataURI("data:image/png;base64,"+strings.Repeat("a", 256), "image/png"),
		}},
		{Role: llm.RoleAssistant, Content: strings.Repeat("reasoning", 200)},
	}
	compactor := tinyBudgetTiered()
	var savings int
	allocs := testing.AllocsPerRun(100, func() {
		savings = compactor.WouldReduceBytes(history, 0)
	})
	if savings <= 0 {
		t.Fatalf("WouldReduceBytes = %d, want positive", savings)
	}
	if allocs != 0 {
		t.Fatalf("WouldReduceBytes allocations = %v, want 0", allocs)
	}
}

func TestTiered_PreservesSystemAndUserHead(t *testing.T) {
	t.Parallel()
	c := tinyBudgetTiered()
	in := []llm.Message{
		{Role: "system", Content: "load-bearing system prompt"},
		{Role: "user", Content: "original user request — must not be trimmed"},
		{Role: "tool", ToolCallID: "t1", Content: strings.Repeat("x", 800)},
		{Role: "assistant", Content: "reply"},
	}
	res, err := c.Compact(t.Context(), in, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.History[0].Content != "load-bearing system prompt" {
		t.Errorf("system message altered: %q", res.History[0].Content)
	}
	// The user message sits in the older window (it's not in keepRecent=1).
	// It must STILL be preserved verbatim — Tiered never touches user
	// messages.
	if res.History[1].Content != "original user request — must not be trimmed" {
		t.Errorf("user message altered: %q", res.History[1].Content)
	}
}

func TestTiered_PreservesKeepRecentWindow(t *testing.T) {
	t.Parallel()
	c := tinyBudgetTiered()
	in := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "tool", ToolCallID: "t1", Content: strings.Repeat("a", 700)},
		{Role: "assistant", Content: "recent reasoning that must survive verbatim"},
		{Role: "tool", ToolCallID: "t2", Content: strings.Repeat("b", 700)},
	}
	res, err := c.Compact(t.Context(), in, 2) // keep last 2
	if err != nil {
		t.Fatal(err)
	}
	if res.History[3].Content != "recent reasoning that must survive verbatim" {
		t.Errorf("recent assistant trimmed: %q", res.History[3].Content)
	}
	if len(res.History[4].Content) < 700 {
		t.Errorf("recent tool result trimmed: len=%d", len(res.History[4].Content))
	}
}

func TestTiered_PreservesToolCallsOnAssistantMessages(t *testing.T) {
	t.Parallel()
	c := tinyBudgetTiered()
	in := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{
			Role:    "assistant",
			Content: strings.Repeat("r", 800),
			ToolCalls: []llm.ToolCall{{
				ID:   "tc1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "read",
					Arguments: `{"path":"foo.go"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "tc1", Content: strings.Repeat("a", 800)},
		{Role: "user", Content: "next"},
	}
	res, err := c.Compact(t.Context(), in, 1) // keep last 1
	if err != nil {
		t.Fatal(err)
	}
	// Tool calls on the assistant message must survive every phase
	// — that's the action trail. Only Content (reasoning) gets cut.
	if len(res.History[2].ToolCalls) != 1 {
		t.Fatalf("ToolCalls dropped: %v", res.History[2].ToolCalls)
	}
	if res.History[2].ToolCalls[0].ID != "tc1" {
		t.Errorf("ToolCall id altered: %q", res.History[2].ToolCalls[0].ID)
	}
	if res.History[2].ToolCalls[0].Function.Arguments != `{"path":"foo.go"}` {
		t.Errorf("ToolCall arguments altered: %q", res.History[2].ToolCalls[0].Function.Arguments)
	}
}

func TestTiered_DefaultsApplied(t *testing.T) {
	t.Parallel()
	c := compact.NewTiered(0)
	if c.TargetBytes != compact.TieredDefaultTargetBytes {
		t.Errorf("TargetBytes = %d, want %d", c.TargetBytes, compact.TieredDefaultTargetBytes)
	}
	if c.Phase1Threshold != 0.60 || c.Phase2Threshold != 0.75 || c.Phase3Threshold != 0.90 {
		t.Errorf("default thresholds: got (%v,%v,%v), want (0.60,0.75,0.90)",
			c.Phase1Threshold, c.Phase2Threshold, c.Phase3Threshold)
	}
}

func TestTiered_WindowSizesBudget(t *testing.T) {
	t.Parallel()
	// 1M-token window → 4 chars/token × tokens / 2 = 2,000,000-byte
	// budget. Phase 1 trigger lands at 60% of that (~1.2MB) rather than
	// at 78KB (the old hardcoded-default trigger that fired every
	// iteration on large-context models).
	c := compact.NewTiered(1_000_000)
	if got, want := c.TargetBytes, 1_000_000*4/2; got != want {
		t.Errorf("TargetBytes for 1M window = %d, want %d", got, want)
	}
}

func TestTiered_TinyWindowClampsToDefault(t *testing.T) {
	t.Parallel()
	// 4k window would compute an 8KB budget; clamp to the package
	// default so a misconfigured window doesn't yield a Phase 1
	// trigger small enough to fire on every iteration.
	c := compact.NewTiered(4_000)
	if c.TargetBytes != compact.TieredDefaultTargetBytes {
		t.Errorf("tiny window: TargetBytes = %d, want clamp to %d",
			c.TargetBytes, compact.TieredDefaultTargetBytes)
	}
}

func TestTiered_ZeroFieldsFallBackToDefaults(t *testing.T) {
	t.Parallel()
	// Caller constructs a zero-value Tiered{} — every field zero.
	// The engine should still operate against the package defaults.
	c := &compact.Tiered{}
	in := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u"},
		{Role: "assistant", Content: "a"},
	}
	res, err := c.Compact(t.Context(), in, 2)
	if err != nil {
		t.Fatalf("zero-value Tiered errored: %v", err)
	}
	if res.Engine != "tiered" {
		t.Errorf("Engine = %q, want tiered", res.Engine)
	}
}
