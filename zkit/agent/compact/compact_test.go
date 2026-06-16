package compact_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// --- structural ---

func TestStructural_ShortHistoryPassesThrough(t *testing.T) {
	t.Parallel()
	c := compact.NewStructural()
	in := []llm.Message{
		{Role: "user", Content: "do the thing"},
		{Role: "assistant", Content: "ok"},
	}
	res, err := c.Compact(t.Context(), in, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.History) != len(in) {
		t.Errorf("len = %d, want %d", len(res.History), len(in))
	}
	if res.Engine != "structural" {
		t.Errorf("Engine = %q, want structural", res.Engine)
	}
}

func TestStructural_TrimsLargeToolResults(t *testing.T) {
	t.Parallel()
	c := compact.NewStructural()
	bigBody := strings.Repeat("x", compact.ToolResultTrimAt+200)
	in := []llm.Message{
		{Role: "user", Content: "read foo"},
		{Role: "tool", ToolCallID: "1", Content: bigBody},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "next"},
	}
	res, err := c.Compact(t.Context(), in, 2) // keep last 2 (assistant + user)
	if err != nil {
		t.Fatal(err)
	}
	// Older slice was [user, tool]. The tool result must have been elided.
	if got := res.History[1].Content; len(got) >= compact.ToolResultTrimAt {
		t.Errorf("tool result not trimmed: got %d chars", len(got))
	}
	if res.History[1].ToolCallID != "1" {
		t.Errorf("ToolCallID dropped during trim")
	}
	if res.Warning == "" {
		t.Errorf("Warning should describe what was trimmed")
	}
}

func TestStructural_NeverTrimsUserMessages(t *testing.T) {
	t.Parallel()
	c := compact.NewStructural()
	bigPrompt := strings.Repeat("y", 5000)
	in := []llm.Message{
		{Role: "user", Content: bigPrompt},
		{Role: "assistant", Content: "ack"},
		{Role: "user", Content: "next"},
	}
	res, err := c.Compact(t.Context(), in, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.History[0].Content != bigPrompt {
		t.Errorf("user message was trimmed; load-bearing intent must survive")
	}
}

// --- summary ---

// fakeProvider implements just enough of [llm.Provider] for the
// summary test: a Complete that emits a single chunk and closes.
type fakeProvider struct {
	llm.Provider // embed to inherit nil methods; we only override Complete
	out          string
}

func (f fakeProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: f.out}, nil)
	}, nil
}

// Regression: the naive tail-cut put a tool message at the head of
// `recent` while the assistant message owning its tool_call went into
// `older` and was summarised away. The resulting history sent a
// dangling tool result to the LLM, producing "no tool call found for
// function call output with call_id …" on the next request.
//
// After the fix, splitForSummary walks tail backward past any tool
// messages so the anchoring assistant turn lives in `recent` along
// with all its tool responses.
func TestSummary_PreservesToolCallAnchorAcrossBoundary(t *testing.T) {
	t.Parallel()
	in := []llm.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "do the thing"},
		// This assistant turn opens a tool call. Naive keepRecent=2
		// would cut after the assistant, putting [tool, user-followup]
		// in recent and orphaning call_X — exactly the codex 400 we
		// hit in production.
		{
			Role:    "assistant",
			Content: "calling search",
			ToolCalls: []llm.ToolCall{{
				ID: "call_X", Type: "function",
				Function: llm.ToolCallFunction{Name: "search", Arguments: `{"q":"x"}`},
			}},
		},
		{Role: "tool", ToolCallID: "call_X", Content: "search result blob"},
		{Role: "user", Content: "yes go"},
	}
	c := compact.NewSummary(fakeProvider{out: "Summary."}, "tiny-llm")

	res, err := c.Compact(t.Context(), in, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Walk the result and confirm every role="tool" message has its
	// owning assistant earlier in the slice. That's the invariant the
	// LLM APIs require.
	declared := map[string]bool{}
	for _, m := range res.History {
		for _, tc := range m.ToolCalls {
			declared[tc.ID] = true
		}
		if m.Role == "tool" {
			if !declared[m.ToolCallID] {
				t.Errorf("tool message ToolCallID=%q has no anchoring assistant message in result; history=%+v",
					m.ToolCallID, res.History)
			}
		}
	}
}

func TestSummary_ReplacesOlderWithSummary(t *testing.T) {
	t.Parallel()
	in := []llm.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "build a thing"},
		{Role: "assistant", Content: "reading files"},
		{Role: "tool", ToolCallID: "1", Content: strings.Repeat("file content ", 200)},
		{Role: "assistant", Content: "ok, here's the plan…"},
		{Role: "user", Content: "yes go"},
		{Role: "assistant", Content: "[turn N] working"},
	}
	c := compact.NewSummary(fakeProvider{out: "Summary of the older turns."}, "tiny-llm")

	res, err := c.Compact(t.Context(), in, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Expected shape: [system, summary-as-assistant, user, assistant]
	if len(res.History) != 4 {
		t.Fatalf("len = %d, want 4 (system + summary + 2 recent), got %+v", len(res.History), res.History)
	}
	if res.History[0].Role != "system" {
		t.Errorf("leading system message missing or wrong: %+v", res.History[0])
	}
	if res.History[1].Role != "assistant" || !strings.Contains(res.History[1].Content, "Summary of the older turns.") {
		t.Errorf("summary message missing model output: %+v", res.History[1])
	}
	if res.Engine != "summary" {
		t.Errorf("Engine = %q, want summary", res.Engine)
	}
}

// errorProvider returns an immediate stream error to verify Compact
// surfaces provider failures rather than silently no-op'ing.
type errorProvider struct {
	llm.Provider
}

func (errorProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{}, errors.New("simulated stream failure"))
	}, nil
}

func TestSummary_ProviderFailurePropagates(t *testing.T) {
	t.Parallel()
	c := compact.NewSummary(errorProvider{}, "tiny-llm")
	_, err := c.Compact(t.Context(), []llm.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
		{Role: "assistant", Content: "d"},
	}, 1)
	if err == nil {
		t.Fatal("expected error from failing provider, got nil")
	}
	if !strings.Contains(err.Error(), "simulated stream failure") {
		t.Errorf("error didn't propagate provider message: %v", err)
	}
}

// --- engine name parser ---

func TestParseEngine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in    string
		want  string
		errOK bool
	}{
		{"", "structural", false},
		{"structural", "structural", false},
		{"tiered", "tiered", false},
		{"summary", "summary", false},
		{"executive", "executive", false},
		{"unknown", "", true},
	} {
		got, err := compact.ParseEngine(c.in)
		if c.errOK {
			if err == nil {
				t.Errorf("ParseEngine(%q) expected error, got nil", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseEngine(%q) unexpected err: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseEngine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Probe gate: Structural's WouldReduceBytes is the cheap predicate the
// shell's proactive-compaction trigger consults so a token-pressured
// but already-lean history doesn't burn a no-op compaction cycle.

func TestStructural_WouldReduceBytes_NothingToTrim(t *testing.T) {
	t.Parallel()
	// All older messages well under the trim thresholds — the engine
	// would just copy them unchanged. Probe must say zero.
	history := []llm.Message{
		{Role: "user", Content: "short prompt"},
		{Role: "assistant", Content: "short answer"},
		{Role: "tool", Content: "ok"},
		{Role: "user", Content: "follow up"},
		{Role: "assistant", Content: "another short answer"},
	}
	got := compact.Structural{}.WouldReduceBytes(history, 2)
	if got != 0 {
		t.Errorf("WouldReduceBytes on lean history = %d, want 0", got)
	}
}

func TestStructural_WouldReduceBytes_LargeToolResult(t *testing.T) {
	t.Parallel()
	// One oversized older tool result — probe must report positive
	// savings (and Compact must actually deliver them).
	bulk := strings.Repeat("x", 2048) // 2KB > ToolResultTrimAt (512)
	history := []llm.Message{
		{Role: "user", Content: "do the thing"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: bulk},
		{Role: "user", Content: "follow up"},
		{Role: "assistant", Content: "ok"},
	}
	probe := compact.Structural{}.WouldReduceBytes(history, 2)
	if probe <= 0 {
		t.Fatalf("WouldReduceBytes with 2KB tool result = %d, want positive", probe)
	}
	res, err := compact.Structural{}.Compact(t.Context(), history, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.BytesTrimmed == 0 {
		t.Errorf("Compact returned BytesTrimmed=0 but probe was positive (%d)", probe)
	}
}

func TestStructural_WouldReduceBytes_ShortHistory(t *testing.T) {
	t.Parallel()
	// len(history) <= keepRecent → nothing older to even look at.
	history := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	if got := (compact.Structural{}).WouldReduceBytes(history, 4); got != 0 {
		t.Errorf("WouldReduceBytes with keep >= len = %d, want 0", got)
	}
}

// Sanity: the optional Prober contract — Structural satisfies it so
// the shell can type-assert without a build-time dependency. Locks
// the interface against accidental removal.
func TestStructural_SatisfiesProber(t *testing.T) {
	t.Parallel()
	var _ compact.Prober = compact.Structural{}
}

func TestPruneOrphanToolResults_KeepsValidPairs(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		{Role: "user", Content: "search"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_1", Function: llm.ToolCallFunction{Name: "search"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
		{Role: "assistant", Content: "done"},
	}
	out, dropped := compact.PruneOrphanToolResults(history)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if len(out) != len(history) {
		t.Errorf("len(out) = %d, want %d", len(out), len(history))
	}
}

func TestPruneOrphanToolResults_DropsOrphan(t *testing.T) {
	t.Parallel()
	// Scenario: an earlier compaction summarised away an assistant
	// whose tool_calls included call_GHOST. The matching tool result
	// survived in recent. The sweep must drop the orphan.
	history := []llm.Message{
		{Role: "assistant", Content: "[compacted summary of N older messages]"},
		{Role: "tool", ToolCallID: "call_GHOST", Content: "stale result"},
		{Role: "assistant", Content: "carrying on"},
	}
	out, dropped := compact.PruneOrphanToolResults(history)
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if len(out) != 2 {
		t.Errorf("len(out) = %d, want 2", len(out))
	}
	for _, m := range out {
		if m.Role == "tool" {
			t.Errorf("orphan tool message survived sweep: %+v", m)
		}
	}
}

func TestPruneOrphanToolResults_DropsEmptyToolCallID(t *testing.T) {
	t.Parallel()
	// A tool message with no ToolCallID can never be paired by any
	// future inspection. Drop it before the provider rejects.
	history := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolCallID: "", Content: "stray"},
	}
	out, dropped := compact.PruneOrphanToolResults(history)
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if len(out) != 1 {
		t.Errorf("len(out) = %d, want 1", len(out))
	}
}

func TestPruneOrphanToolResults_KeepsAllParallelResults(t *testing.T) {
	t.Parallel()
	// One assistant message can carry multiple parallel tool calls;
	// every paired result must survive the sweep.
	history := []llm.Message{
		{Role: "user", Content: "do things"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_A", Function: llm.ToolCallFunction{Name: "read"}},
			{ID: "call_B", Function: llm.ToolCallFunction{Name: "grep"}},
			{ID: "call_C", Function: llm.ToolCallFunction{Name: "ls"}},
		}},
		{Role: "tool", ToolCallID: "call_A", Content: "file body"},
		{Role: "tool", ToolCallID: "call_B", Content: "grep hits"},
		{Role: "tool", ToolCallID: "call_C", Content: "dir list"},
	}
	out, dropped := compact.PruneOrphanToolResults(history)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0; parallel results must all survive", dropped)
	}
	if len(out) != len(history) {
		t.Errorf("len(out) = %d, want %d", len(out), len(history))
	}
}

func TestPruneOrphanToolResults_OnlyKnowsCallsPrecedingTheResult(t *testing.T) {
	t.Parallel()
	// Defence-in-depth: a tool result that appears BEFORE its
	// "owning" assistant in the slice is an out-of-order layout the
	// provider would reject. The sweep drops it because nothing
	// preceding has the call_id.
	history := []llm.Message{
		{Role: "tool", ToolCallID: "call_X", Content: "way too early"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_X", Function: llm.ToolCallFunction{Name: "later"}},
		}},
	}
	out, dropped := compact.PruneOrphanToolResults(history)
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if len(out) != 1 || out[0].Role != "assistant" {
		t.Errorf("out = %+v, want one assistant only", out)
	}
}

func TestRepairToolPairing_StripsUnansweredAssistantCall(t *testing.T) {
	t.Parallel()
	// A restored/partial transcript where the last assistant tool_use never
	// got its result. The dangling call must be stripped, and the assistant
	// turn dropped because it had nothing else.
	history := []llm.Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_1", Function: llm.ToolCallFunction{Name: "read"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "body"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_2", Function: llm.ToolCallFunction{Name: "edit"}}}},
		{Role: "user", Content: "next"},
	}
	out, changed := compact.RepairToolPairing(history)
	if changed != 1 {
		t.Errorf("changed = %d, want 1 (one stripped unanswered call)", changed)
	}
	for _, m := range out {
		for _, tc := range m.ToolCalls {
			if tc.ID == "call_2" {
				t.Errorf("unanswered call_2 survived repair: %+v", m)
			}
		}
	}
	// The call_1 pair and both user/assistant content turns survive.
	if len(out) != 4 {
		t.Errorf("len(out) = %d, want 4 (empty assistant turn dropped)", len(out))
	}
}

func TestRepairToolPairing_KeepsContentWhenStrippingPartialCalls(t *testing.T) {
	t.Parallel()
	// An assistant turn with text + two calls, only one answered. The turn is
	// kept (it has content), the answered call stays, the orphan is stripped.
	history := []llm.Message{
		{Role: "assistant", Content: "working", ToolCalls: []llm.ToolCall{
			{ID: "call_A", Function: llm.ToolCallFunction{Name: "read"}},
			{ID: "call_B", Function: llm.ToolCallFunction{Name: "edit"}},
		}},
		{Role: "tool", ToolCallID: "call_A", Content: "body"},
	}
	out, changed := compact.RepairToolPairing(history)
	if changed != 1 {
		t.Errorf("changed = %d, want 1", changed)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Content != "working" {
		t.Errorf("assistant content lost: %q", out[0].Content)
	}
	if len(out[0].ToolCalls) != 1 || out[0].ToolCalls[0].ID != "call_A" {
		t.Errorf("expected only the answered call_A to survive, got %+v", out[0].ToolCalls)
	}
}

func TestRepairToolPairing_HandlesBothDirections(t *testing.T) {
	t.Parallel()
	// Orphan result (call summarised away) AND orphan call (result missing)
	// in the same transcript — both must be repaired.
	history := []llm.Message{
		{Role: "tool", ToolCallID: "ghost", Content: "stale"},                                                      // orphan result
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_X", Function: llm.ToolCallFunction{Name: "ls"}}}}, // orphan call
		{Role: "user", Content: "ok"},
	}
	out, changed := compact.RepairToolPairing(history)
	if changed != 2 {
		t.Errorf("changed = %d, want 2 (one orphan result + one orphan call)", changed)
	}
	if len(out) != 1 || out[0].Role != "user" {
		t.Errorf("out = %+v, want only the user message", out)
	}
}

func TestRepairToolPairing_NoChangeOnCleanHistory(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Function: llm.ToolCallFunction{Name: "read"}}}},
		{Role: "tool", ToolCallID: "c1", Content: "body"},
		{Role: "assistant", Content: "done"},
	}
	out, changed := compact.RepairToolPairing(history)
	if changed != 0 {
		t.Errorf("changed = %d, want 0 on clean history", changed)
	}
	if len(out) != len(history) {
		t.Errorf("len(out) = %d, want %d", len(out), len(history))
	}
}

// Structural's per-role thresholds are configurable via the
// AssistantTrimAt / ToolTrimAt fields with the package constants
// (AssistantContentTrimAt / ToolResultTrimAt) as zero-value
// fallbacks. The roast called the magic-constant heuristic "because
// I said so" engineering; the override path lets a small-context
// model tighten the heuristic without forking the engine.
func TestStructural_OverriddenThresholds(t *testing.T) {
	t.Parallel()
	// Tight 200-byte assistant threshold: a 300-byte message gets
	// trimmed under the override but stayed verbatim under the
	// default (1024).
	body := strings.Repeat("a", 300)
	history := []llm.Message{
		{Role: "user", Content: "ping"},
		{Role: "assistant", Content: body},
		{Role: "user", Content: "follow up"},
		{Role: "assistant", Content: "ok"},
	}
	def := compact.Structural{}
	if got := def.WouldReduceBytes(history, 2); got != 0 {
		t.Errorf("default thresholds: %d bytes savable, want 0 (300 < 1024)", got)
	}
	tight := compact.Structural{AssistantTrimAt: 200}
	if got := tight.WouldReduceBytes(history, 2); got <= 0 {
		t.Errorf("tight assistant threshold: %d, want positive", got)
	}
	res, err := tight.Compact(t.Context(), history, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.BytesTrimmed == 0 {
		t.Errorf("tight threshold should have trimmed something; result: %+v", res)
	}
}
