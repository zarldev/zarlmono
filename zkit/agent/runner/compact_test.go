package runner_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const inPlaceSentinel = "<<COMPACTED>>"

// inPlaceCompactor models Tiered / Structural: it trims message
// content in place and reports BytesTrimmed, but never changes the
// message count. Each call it prepends a sentinel to the oldest
// non-system message (a stable target — the prompt is never in the
// dropped tail) so a later call can detect whether the runner carried
// the trimmed history forward.
type inPlaceCompactor struct {
	calls            atomic.Int32
	sawSentinelAgain atomic.Bool // set if a later call sees a prior call's sentinel
}

func (c *inPlaceCompactor) Compact(_ context.Context, msgs []llm.Message, _ int) (compact.Result, error) {
	if c.calls.Add(1) >= 2 {
		for _, m := range msgs {
			if strings.Contains(m.Content, inPlaceSentinel) {
				c.sawSentinelAgain.Store(true)
				break
			}
		}
	}
	out := append([]llm.Message{}, msgs...)
	for i := range out {
		if out[i].Role == llm.RoleSystem {
			continue
		}
		out[i].Content = inPlaceSentinel + out[i].Content
		break
	}
	// Same message count, positive BytesTrimmed — the in-place case the
	// length-only swap-in guard used to discard.
	return compact.Result{History: out, BytesTrimmed: len(inPlaceSentinel), Engine: compact.EngineTiered}, nil
}

// trackingCompactor records every Compact call and optionally trims
// the message slice down to its tail-N entries. Satisfies the unified
// compact.Compactor interface — same shape the shell uses.
type trackingCompactor struct {
	calls atomic.Int32
	keep  int // 0 = no-op
}

func (c *trackingCompactor) Compact(_ context.Context, msgs []llm.Message, _ int) (compact.Result, error) {
	c.calls.Add(1)
	if c.keep <= 0 || len(msgs) <= c.keep {
		return compact.Result{History: msgs}, nil
	}
	// Always keep a leading system message if present.
	if len(msgs) > 0 && msgs[0].Role == "system" {
		out := append([]llm.Message{msgs[0]}, msgs[len(msgs)-c.keep:]...)
		return compact.Result{History: out}, nil
	}
	return compact.Result{History: msgs[len(msgs)-c.keep:]}, nil
}

func TestRun_CompactorSkippedOnFirstIteration(t *testing.T) {
	t.Parallel()

	// One-turn run that completes on iteration 0 — compactor must
	// not be invoked.
	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{{chunkText("done"), chunkDone()}},
	}
	reg := newRegistry()
	c := &trackingCompactor{}

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg),
		runner.WithMaxIterations(3),
		runner.WithCompactor(c),
	)
	res := r.Run(context.Background(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "ping",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if got := c.calls.Load(); got != 0 {
		t.Errorf("compactor called %d times on a 1-iteration run; want 0", got)
	}
}

func TestRun_CompactorInvokedBetweenIterations(t *testing.T) {
	t.Parallel()

	// Three iterations: tool, tool, text.
	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkToolCall("a", "echo", `{}`), chunkDone()},
			{chunkToolCall("b", "echo", `{}`), chunkDone()},
			{chunkText("done"), chunkDone()},
		},
	}
	reg := newRegistry(stubTool{name: "echo"})
	c := &trackingCompactor{}
	sink := newRecordingSink()

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg),
		runner.WithMaxIterations(5),
		runner.WithCompactor(c),
		runner.WithSink(sink),
	)
	res := r.Run(context.Background(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	// Iteration 0: skipped; iter 1: called; iter 2: called → 2 calls.
	if got := c.calls.Load(); got != 2 {
		t.Errorf("compactor calls = %d, want 2", got)
	}
	// trackingCompactor with keep=0 returns changed=false; no event.
	if got := len(sink.compactions); got != 0 {
		t.Errorf("CompactionApplied fired %d times; want 0 (compactor was a no-op)", got)
	}
}

func TestRun_CompactorAppliesAndPublishesEvent(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkToolCall("a", "echo", `{}`), chunkDone()},
			{chunkToolCall("b", "echo", `{}`), chunkDone()},
			{chunkText("done"), chunkDone()},
		},
	}
	reg := newRegistry(stubTool{name: "echo"})
	c := &trackingCompactor{keep: 1} // keep last message only
	sink := newRecordingSink()

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg),
		runner.WithMaxIterations(5),
		runner.WithCompactor(c),
		runner.WithSink(sink),
	)
	res := r.Run(context.Background(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if got := len(sink.compactions); got == 0 {
		t.Errorf("CompactionApplied never fired despite compactor returning changed=true")
	}
	for _, ev := range sink.compactions {
		if ev.MessagesAfter >= ev.MessagesBefore {
			t.Errorf("CompactionApplied: after=%d not less than before=%d", ev.MessagesAfter, ev.MessagesBefore)
		}
	}
}

// TestRun_InPlaceCompactionIsAdopted is the regression guard for the
// "compacted N→N forever" loop: a compactor that trims content in
// place (Tiered / Structural) returns the same message count, so the
// old length-only swap-in guard discarded the trimmed history every
// iteration. The runner kept the full-size slice, pressure never
// relented, and the engine re-fired (and re-published "compacted") on
// every turn while tokens climbed. The fix swaps in result.History
// whenever the engine reports work (BytesTrimmed > 0), matching the
// publish guard.
func TestRun_InPlaceCompactionIsAdopted(t *testing.T) {
	t.Parallel()

	// Three iterations → two compact calls (iter 0 skipped).
	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkToolCall("a", "echo", `{}`), chunkDone()},
			{chunkToolCall("b", "echo", `{}`), chunkDone()},
			{chunkText("done"), chunkDone()},
		},
	}
	reg := newRegistry(stubTool{name: "echo"})
	c := &inPlaceCompactor{}
	sink := newRecordingSink()

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg),
		runner.WithMaxIterations(5),
		runner.WithCompactor(c),
		runner.WithSink(sink),
	)
	res := r.Run(context.Background(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if c.calls.Load() < 2 {
		t.Fatalf("compactor called %d times; need >= 2 to exercise adoption", c.calls.Load())
	}
	// The crux: the second call must see the first call's in-place
	// trim, proving the runner carried result.History forward. Under
	// the old length-only guard the sentinel was discarded each time.
	if !c.sawSentinelAgain.Load() {
		t.Error("in-place compaction was discarded: second Compact call did not see the first call's trim")
	}
	// An in-place trim still publishes — BytesTrimmed > 0 is real work.
	if len(sink.compactions) == 0 {
		t.Error("CompactionApplied never fired for an in-place trim with BytesTrimmed > 0")
	}
}
