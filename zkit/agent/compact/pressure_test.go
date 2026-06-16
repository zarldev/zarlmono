package compact_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// recordingCompactor counts Compact calls and reports a fixed
// WouldReduceBytes so we can prove the gate is intercepting before
// the inner engine sees the call.
type recordingCompactor struct {
	wouldReduce int
	calls       int
}

func (r *recordingCompactor) Compact(_ context.Context, h []llm.Message, _ int) (compact.Result, error) {
	r.calls++
	return compact.Result{History: h, Engine: "test"}, nil
}

func (r *recordingCompactor) WouldReduceBytes(_ []llm.Message, _ int) int {
	return r.wouldReduce
}

// Under threshold → gate returns 0 regardless of what the inner
// prober claims is savable. The runner's per-iteration check skips
// Compact when WouldReduceBytes ≤ 0, so the inner engine never gets
// called for a no-pressure scenario.
func TestPressureGated_ClosedWhenUsageUnderThreshold(t *testing.T) {
	t.Parallel()
	inner := &recordingCompactor{wouldReduce: 999_999}
	g := &compact.PressureGated{
		Inner:   inner,
		Window:  1_000_000,
		Reserve: 16_000,
	}
	g.ObserveUsage(&llm.Usage{TotalTokens: 11_000}) // 1.1% of a 1M window
	if got := g.WouldReduceBytes(nil, 4); got != 0 {
		t.Errorf("WouldReduceBytes = %d, want 0 (under threshold)", got)
	}
	if inner.calls != 0 {
		t.Errorf("inner Compact called %d times; gate should suppress", inner.calls)
	}
}

// Over threshold → gate forwards to inner prober. The user's
// pressure is real; the runner should run Compact.
func TestPressureGated_OpenWhenUsageOverThreshold(t *testing.T) {
	t.Parallel()
	inner := &recordingCompactor{wouldReduce: 12_345}
	g := &compact.PressureGated{
		Inner:   inner,
		Window:  1_000_000,
		Reserve: 16_000,
	}
	g.ObserveUsage(&llm.Usage{TotalTokens: 990_000}) // > 984k threshold
	if got := g.WouldReduceBytes(nil, 4); got != 12_345 {
		t.Errorf("WouldReduceBytes = %d, want 12345 (forwarded to inner)", got)
	}
}

// No usage observed yet (first turn) → gate closed. The consumer is
// responsible for proactive compaction before the first turn; the
// runner's per-iteration gate has no signal to act on.
func TestPressureGated_ClosedOnNilUsage(t *testing.T) {
	t.Parallel()
	inner := &recordingCompactor{wouldReduce: 999_999}
	g := &compact.PressureGated{
		Inner:   inner,
		Window:  1_000_000,
		Reserve: 16_000,
	}
	// Deliberately no ObserveUsage call — simulates first-turn state.
	if got := g.WouldReduceBytes(nil, 4); got != 0 {
		t.Errorf("WouldReduceBytes = %d, want 0 (no observation)", got)
	}
}

// Zero window → gate falls back to delegating to the inner prober.
// This is the safe default for callers that haven't configured the
// window (tests, headless modes without a configured model).
func TestPressureGated_DelegatesWhenWindowUnknown(t *testing.T) {
	t.Parallel()
	inner := &recordingCompactor{wouldReduce: 4_242}
	g := &compact.PressureGated{
		Inner:   inner,
		Window:  0,
		Reserve: 0,
	}
	g.ObserveUsage(&llm.Usage{TotalTokens: 50_000})
	if got := g.WouldReduceBytes(nil, 4); got != 4_242 {
		t.Errorf("WouldReduceBytes = %d, want 4242 (delegated to inner)", got)
	}
}

// Reserve ≥ window → threshold ≤ 0 → gate closed (compaction
// disabled by configuration).
func TestPressureGated_ClosedWhenThresholdNonPositive(t *testing.T) {
	t.Parallel()
	inner := &recordingCompactor{wouldReduce: 999_999}
	g := &compact.PressureGated{
		Inner:   inner,
		Window:  4_000,
		Reserve: 4_000, // threshold = 0
	}
	g.ObserveUsage(&llm.Usage{TotalTokens: 50_000})
	if got := g.WouldReduceBytes(nil, 4); got != 0 {
		t.Errorf("WouldReduceBytes = %d, want 0 (threshold non-positive)", got)
	}
}

// Compact always passes through to the inner engine — the gate is
// strictly at the prober level. Once the runner decides to call
// Compact (either because the gate opened, or because no prober
// existed), the wrapper trusts that decision.
func TestPressureGated_CompactAlwaysDelegates(t *testing.T) {
	t.Parallel()
	inner := &recordingCompactor{wouldReduce: 0}
	g := &compact.PressureGated{
		Inner:   inner,
		Window:  1_000_000,
		Reserve: 16_000,
	}
	g.ObserveUsage(&llm.Usage{TotalTokens: 11_000})
	if _, err := g.Compact(t.Context(), nil, 4); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if inner.calls != 1 {
		t.Errorf("inner.calls = %d, want 1 (Compact must delegate)", inner.calls)
	}
}

// ObserveUsage with nil leaves a previously-stored observation
// in place. Providers occasionally drop usage on a chunk (llama.cpp
// is known to skip it on the terminal chunk) — those should not
// silently reset the gate to "no signal yet."
func TestPressureGated_ObserveNilPreservesLast(t *testing.T) {
	t.Parallel()
	inner := &recordingCompactor{wouldReduce: 7_777}
	g := &compact.PressureGated{
		Inner:   inner,
		Window:  1_000_000,
		Reserve: 16_000,
	}
	g.ObserveUsage(&llm.Usage{TotalTokens: 990_000})
	g.ObserveUsage(nil) // simulates a chunk with no usage
	if got := g.WouldReduceBytes(nil, 4); got != 7_777 {
		t.Errorf("WouldReduceBytes = %d, want 7777 (nil observation should not erase last)", got)
	}
}

// Implements is a compile-time check that PressureGated satisfies
// UsageObserver — the optional interface the runner type-asserts
// against to decide whether to publish usage observations to the
// compactor. If this fails to compile, the runner's wiring is
// silently broken.
var _ compact.UsageObserver = (*compact.PressureGated)(nil)
