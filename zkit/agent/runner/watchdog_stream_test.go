package runner_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// streamForeverProvider yields chunks until the consumer stops the sequence.
type streamForeverProvider struct{ thinkingOnly bool }

func (p streamForeverProvider) Name() string { return "stream-forever" }

func (p streamForeverProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			chunk := llm.CompletionChunk{Content: "tok "}
			if p.thinkingOnly {
				chunk = llm.CompletionChunk{Thinking: "reasoning "}
			}
			if !yield(chunk, nil) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}

func TestRunner_IterationTimeoutBoundsStreamingGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := runner.New(
			runner.ClientFromProvider(streamForeverProvider{thinkingOnly: true}),
			runner.WithTools(tools.NewRegistry()),
			runner.WithMaxIterations(1),
			runner.WithIterationTimeout(200*time.Millisecond),
			runner.WithStreamIdleTimeout(10*time.Second),
			runner.WithEmptyStreamBackoff(0),
		)

		done := make(chan runner.TaskResult, 1)
		go func() {
			done <- r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
		}()
		// Advance synctest's fake clock to the configured iteration deadline.
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		res := <-done
		if res.Cause != runner.TerminalCauseIterationTimeout {
			t.Errorf("Cause = %q, want %q", res.Cause, runner.TerminalCauseIterationTimeout)
		}
	})
}

func TestRunner_ThinkingBudgetCutsStuckReasoning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := runner.New(
			runner.ClientFromProvider(streamForeverProvider{thinkingOnly: true}),
			runner.WithTools(tools.NewRegistry()),
			runner.WithMaxIterations(20),
			runner.WithIterationTimeout(30*time.Second),
			runner.WithStreamIdleTimeout(30*time.Second),
			runner.WithThinkingBudget(512),
		)

		res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
		if !errors.Is(res.Err, runner.ErrThinkingBudget) {
			t.Fatalf("Err = %v, want ErrThinkingBudget after the recover limit is spent", res.Err)
		}
	})
}

// thinkThenAnswerProvider streams a little thinking (under any sane budget)
// then a real content answer — the healthy long-reasoning turn the cut
// must NOT touch.
type thinkThenAnswerProvider struct{ calls atomic.Int32 }

func (p *thinkThenAnswerProvider) Name() string { return "think-then-answer" }

func (p *thinkThenAnswerProvider) Complete(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.calls.Add(1)
		for range 5 {
			if !yield(llm.CompletionChunk{Thinking: "step "}, nil) {
				return
			}
		}
		yield(llm.CompletionChunk{Content: "here is the answer"}, nil)
	}
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
