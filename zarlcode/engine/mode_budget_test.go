package engine_test

import (
	"context"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestModeTransitionsRetainIterationTimeAndUsageBudgets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		requests := 0
		live := modeLive(t, root, modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
			return func(yield func(llm.CompletionChunk, error) bool) {
				requests++
				assertModeRequest(t, req, requests%2 == 0)
				time.Sleep(time.Second)
				if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}}, nil) {
					return
				}
				mode := "plan"
				if requests%2 == 0 {
					mode = "build"
				}
				modeCalls(modeCall(strconv.Itoa(requests), mode))(yield)
			}
		}))
		result := live.RunHeadless(t.Context(), "investigate and implement within the budget", 3)
		if result.Reason != runner.TerminalMaxIterations || result.Iterations != 3 || requests != 3 {
			t.Fatalf("result=%+v requests=%d", result, requests)
		}
		// Only transitions followed by an admitted iteration apply; the final
		// pending transition cannot force a fourth request or survive cleanup.
		if mode := live.AppliedMode(); mode.Plan || mode.Generation != 2 {
			t.Fatalf("mode=%+v", mode)
		}
		if result.Duration != 3*time.Second || result.Timing.ProviderDuration != 3*time.Second || result.Timing.TimeToFirstOutput == nil || *result.Timing.TimeToFirstOutput != time.Second {
			t.Fatalf("timing=%+v duration=%s", result.Timing, result.Duration)
		}
		report := engine.Report(result)
		if report.TotalUsage == nil || report.TotalUsage.TotalTokens != 33 || report.Timing.AttemptsWithUsage != 3 || report.UnattributedDuration != 0 {
			t.Fatalf("report=%+v", report)
		}
	})
}
