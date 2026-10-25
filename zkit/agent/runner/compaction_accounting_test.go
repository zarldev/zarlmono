package runner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type compactionAccountingSink struct {
	runner.NopSink
	events []runner.CompactionSettled
}

func (s *compactionAccountingSink) OnCompactionSettled(_ context.Context, e runner.CompactionSettled) {
	s.events = append(s.events, e)
}

func TestCompactionUsageSettlesSeparatelyOnSuccessAndFailure(t *testing.T) {
	failure := errors.New("summarizer unavailable")
	for _, terminal := range []error{nil, failure} {
		sink := &compactionAccountingSink{}
		client := runnertest.NewClient([][]llm.CompletionChunk{
			{runnertest.ChunkToolCall("a", "echo", `{}`), {UsageReported: true, Usage: llm.Usage{PromptTokens: 10}}},
			{runnertest.ChunkText("done")},
		})
		c := compact.Func(func(_ context.Context, history []llm.Message, _ int) (compact.Result, error) {
			return compact.Result{History: history, Inference: &compact.Inference{Provider: "other", Model: "small", Usage: &llm.Usage{PromptTokens: 99}}}, terminal
		})
		result := runner.New(client, runner.WithSink(sink), runner.WithCompactor(c), runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"}))).Run(t.Context(), runner.TaskSpec{ID: "task", Prompt: "go"})
		if !errors.Is(result.Err, terminal) {
			t.Fatalf("Run = %v, want %v", result.Err, terminal)
		}
		if len(sink.events) != 1 || sink.events[0].Inference.Provider != "other" || !errors.Is(sink.events[0].Err, terminal) {
			t.Fatalf("events = %+v", sink.events)
		}
		if result.TotalUsage == nil || result.TotalUsage.PromptTokens != 10 {
			t.Fatalf("compactor usage merged into task usage: %+v", result.TotalUsage)
		}
	}
}

func TestReasoningSubtotalAggregationRequiresCompleteReporting(t *testing.T) {
	for _, reported := range []bool{true, false} {
		client := &cacheUsageClient{usage: []llm.Usage{
			{CompletionTokens: 5, ReasoningTokens: 2, ReasoningTokensReported: true},
			{CompletionTokens: 7, ReasoningTokens: 3, ReasoningTokensReported: reported},
		}}
		result := runner.New(client).Run(t.Context(), runner.TaskSpec{Prompt: "go"})
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		want := 0
		if reported {
			want = 5
		}
		if result.TotalUsage.CompletionTokens != 12 || result.TotalUsage.ReasoningTokens != want || result.TotalUsage.ReasoningTokensReported != reported {
			t.Errorf("usage = %+v", result.TotalUsage)
		}
	}
}
