package pursue_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAttemptTimeoutPreservesSettledEvidence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		partialErr := errors.New("partial attempt")
		var report pursue.AttemptReport
		result := runner.TaskResult{ID: "attempt", Reason: runner.TerminalCancelled, Err: partialErr, FinalContent: "partial", Iterations: 2, Duration: time.Second, TotalUsage: &llm.Usage{PromptTokens: 17}, Messages: []llm.Message{{Role: llm.RoleAssistant, Content: "partial"}}}
		out := pursue.Drive(t.Context(), pursue.NewRequest(func(ctx context.Context, _ runner.TaskSpec) runner.TaskResult {
			<-ctx.Done()
			return result
		}, runner.TaskSpec{ID: "attempt", Prompt: "work"}), pursue.WithAttemptTimeout(time.Second), pursue.WithOnAttempt(func(r pursue.AttemptReport) { report = r }))
		for _, got := range []runner.TaskResult{out.Result, report.Attempt.Result} {
			if got.ID != result.ID || got.FinalContent != result.FinalContent || got.TotalUsage == nil || got.TotalUsage.PromptTokens != 17 || len(got.Messages) != 1 || got.Iterations != 2 || got.Duration != time.Second {
				t.Fatalf("lost settled result: %#v", got)
			}
			if !errors.Is(got.Err, pursue.ErrAttemptTimeout) || !errors.Is(got.Err, partialErr) || got.Reason != runner.TerminalError {
				t.Fatalf("timeout error = %v, reason = %v", got.Err, got.Reason)
			}
		}
	})
}
