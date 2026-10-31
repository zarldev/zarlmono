package swarm_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestInferenceBudgetJoinsAndIncludesDescendantScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limits := swarm.DefaultLimits()
		limits.Workers, limits.ReportedTokens, limits.Runtime = 1, 10, time.Minute
		owner := swarm.New("accounted", swarm.WithInferenceAccounting(), swarm.WithLimits(limits))
		returned := make(chan struct{})
		_, err := owner.Add(swarm.Agent{ID: "worker", Execute: func(ctx context.Context, a swarm.Activation) runner.TaskResult {
			defer close(returned)
			scope := a.InferenceScope()
			scope.Wrap(taskscope.WithDepth(taskscope.WithID(ctx, "child"), 1), inference.Call{Operation: "planner"}, func(yield func(llm.CompletionChunk, error) bool) {
				yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 8, CompletionTokens: 4}}, nil)
			})(func(llm.CompletionChunk, error) bool { return true })
			<-ctx.Done()
			return runner.TaskResult{ID: a.Spec.ID, Reason: runner.TerminalCancelled, Err: ctx.Err()}
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := owner.Run(t.Context()); !errors.Is(err, swarm.ErrBudget) {
			t.Fatalf("Run=%v", err)
		}
		select {
		case <-returned:
		default:
			t.Fatal("budget returned before activation joined")
		}
		got := owner.InferenceSnapshot()
		if got.Tokens != 12 || got.Settled != 1 || got.Active != 0 {
			t.Fatalf("goal consumption=%+v", got)
		}
		if err := owner.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}
