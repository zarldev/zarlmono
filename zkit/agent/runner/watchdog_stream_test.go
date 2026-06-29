package runner_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// streamForeverProvider yields a chunk every 20ms forever, ignoring ctx —
// a runaway generation that STREAMS steadily (so the stream-idle watchdog
// never fires). The only thing that can bound it is the iteration
// wall-clock timeout. Models that burn their whole budget emitting
// thinking tokens look exactly like this: chunks keep coming, nothing
// useful is produced, and the iteration never ends on its own.
type streamForeverProvider struct{ thinkingOnly bool }

func (p streamForeverProvider) Name() string { return "stream-forever" }

func (p streamForeverProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		for {
			chunk := llm.CompletionChunk{Content: "tok "}
			if p.thinkingOnly {
				chunk = llm.CompletionChunk{Thinking: "reasoning "}
			}
			if !yield(chunk, nil) {
				return // consumer stopped ranging (iterCtx fired) — unwind
			}
			time.Sleep(20 * time.Millisecond)
		}
	}, nil
}

// TestRunner_IterationTimeoutBoundsStreamingGeneration is the decisive
// check for the unseen-run anomaly: a task ran 10m47s on one iteration
// while the iteration cap supposedly applied. A continuously-streaming
// generation must be cut by the iteration timeout even though the idle
// watchdog (gap between chunks) never trips. (The live overrun was timer
// starvation under a saturated box, not broken logic — this proves the
// logic; the token/thinking budgets are the load-independent bound.)
func TestRunner_IterationTimeoutBoundsStreamingGeneration(t *testing.T) {
	t.Parallel()
	r := runner.New(
		runner.ClientFromProvider(streamForeverProvider{thinkingOnly: true}),
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(1),
		runner.WithIterationTimeout(200*time.Millisecond),
		runner.WithStreamIdleTimeout(10*time.Second), // long: must NOT be what saves us
		runner.WithEmptyStreamBackoff(0),
	)

	done := make(chan runner.TaskResult, 1)
	go func() {
		done <- r.Run(t.Context(), runner.TaskSpec{
			ID:     taskscope.ID(uuid.NewString()),
			Prompt: "go",
		})
	}()

	select {
	case res := <-done:
		// It returned promptly — the iteration timeout did its job. (Reason
		// can be max-iterations or error depending on recovery; the property
		// under test is that it TERMINATED quickly, not how it's labelled.)
		t.Logf("bounded: reason=%q err=%v", res.Reason, res.Err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not terminate within 5s on a continuously-streaming generation — " +
			"the iteration timeout does NOT bound a steadily-streaming runaway")
	}
}

// TestRunner_ThinkingBudgetCutsStuckReasoning verifies the content-aware
// early-cut: a turn that streams only thinking past the budget is cut with
// ErrThinkingBudget, nudged, and (after the recover limit) terminates —
// bounding the stuck-thinking loop WITHOUT relying on the wall-clock
// timeout. Timeouts are set long so the thinking budget is unambiguously
// what fires.
func TestRunner_ThinkingBudgetCutsStuckReasoning(t *testing.T) {
	t.Parallel()
	r := runner.New(
		runner.ClientFromProvider(streamForeverProvider{thinkingOnly: true}),
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(20),
		runner.WithIterationTimeout(30*time.Second),
		runner.WithStreamIdleTimeout(30*time.Second),
		runner.WithThinkingBudget(512), // tiny: trips after a few thinking chunks
	)

	done := make(chan runner.TaskResult, 1)
	go func() {
		done <- r.Run(t.Context(), runner.TaskSpec{
			ID:     taskscope.ID(uuid.NewString()),
			Prompt: "go",
		})
	}()

	select {
	case res := <-done:
		if !errors.Is(res.Err, runner.ErrThinkingBudget) {
			t.Fatalf("Err = %v, want ErrThinkingBudget after the recover limit is spent", res.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not terminate within 5s — thinking-only budget did not bound the stuck-reasoning loop")
	}
}

// thinkThenAnswerProvider streams a little thinking (under any sane budget)
// then a real content answer — the healthy long-reasoning turn the cut
// must NOT touch.
type thinkThenAnswerProvider struct{ calls atomic.Int32 }

func (p *thinkThenAnswerProvider) Name() string { return "think-then-answer" }

func (p *thinkThenAnswerProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.calls.Add(1)
		for range 5 {
			if !yield(llm.CompletionChunk{Thinking: "step "}, nil) {
				return
			}
		}
		yield(llm.CompletionChunk{Content: "here is the answer"}, nil)
	}, nil
}

// TestRunner_ThinkingBudgetSparesHealthyTurn confirms a turn that reasons
// briefly then produces real content is never cut, even with the budget
// enabled — the cut is gated on zero content having been emitted.
func TestRunner_ThinkingBudgetSparesHealthyTurn(t *testing.T) {
	t.Parallel()
	r := runner.New(
		runner.ClientFromProvider(&thinkThenAnswerProvider{}),
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(5),
		// Budget comfortably above the brief pre-answer thinking (~25 bytes):
		// healthy reasoning finishes and emits content before tripping.
		runner.WithThinkingBudget(1024),
	)
	res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("Reason = %q, want completed (healthy turn must not be cut)", res.Reason)
	}
	if !strings.Contains(res.FinalContent, "here is the answer") {
		t.Errorf("FinalContent = %q, want the produced answer", res.FinalContent)
	}
}
