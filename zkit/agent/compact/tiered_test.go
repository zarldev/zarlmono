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

func TestTiered_Phase3CollapsesEverything(t *testing.T) {
	t.Parallel()
	c := tinyBudgetTiered()
	// Force phase 3: huge tool result, huge assistant. Even after
	// phase 2 trims the assistant to ~256 chars, the structure plus
	// the tool's truncated content still exceeds 900 bytes... or
	// does it? Need to size carefully.
	in := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "do thing"},
		{Role: "assistant", Content: strings.Repeat("r", 2000)},
		{Role: "tool", ToolCallID: "t1", Content: strings.Repeat("a", 2000)},
		{Role: "assistant", Content: strings.Repeat("s", 2000)},
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
	// Phase 3 must clear assistant content entirely.
	if got := res.History[2].Content; got != "" {
		t.Errorf("phase 3: assistant content should be cleared, got len=%d", len(got))
	}
	if got := res.History[4].Content; got != "" {
		t.Errorf("phase 3: assistant content should be cleared, got len=%d", len(got))
	}
	// Tool messages should be replaced with placeholders, NOT dropped.
	if got := res.History[3].Content; !strings.Contains(got, "[tool result elided") {
		t.Errorf("phase 3: tool body not collapsed: %q", got[:min(50, len(got))])
	}
	// ToolCallID linkage preserved.
	if res.History[3].ToolCallID != "t1" || res.History[5].ToolCallID != "t2" {
		t.Errorf("tool call ids dropped in phase 3")
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
