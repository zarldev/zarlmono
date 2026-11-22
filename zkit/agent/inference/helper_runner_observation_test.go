package inference_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRunnerTriggeredHelperObservationsDoNotChangeTaskTotals(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		account := inference.New()
		scope := inference.Scope{}
		if enabled {
			scope.Account = account
		}
		var verdict guardrails.VerdictObservation
		var plan spawn.PlanObservation
		verdicts, plans := 0, 0
		judge := guardrails.NewLLMVerdictJudge(&usageClient{content: `{"action":"smaller_scope","rationale":"narrow"}`},
			guardrails.WithVerdictInferenceScope(scope, "judge-model"),
			guardrails.WithVerdictObservationSink(func(_ context.Context, o guardrails.VerdictObservation) { verdict = o; verdicts++ }),
		)
		guard := guardrails.NewDecomposeGuardrail(0, guardrails.WithDecomposeJudge(judge))
		childSource := guardrails.NewGuardedSource(tools.NewRegistry(runnertest.Tool{Name: "failing", Err: errors.New("fixture tool failure")}), guard)
		childUsage := llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{TotalTokens: 3}}
		childClient := runnertest.NewClient([][]llm.CompletionChunk{
			{runnertest.ChunkToolCall("first", "failing", `{}`), childUsage},
			{runnertest.ChunkToolCall("second", "failing", `{}`), childUsage},
			{runnertest.ChunkToolCall("third", "failing", `{}`), childUsage},
			{runnertest.ChunkText("child completed"), childUsage},
		})
		child := runner.New(childClient, runner.WithTools(childSource), runner.WithInferenceScope(scope))
		planner := spawn.NewLLMSpawnPlanner(&usageClient{content: `{"agent":"reader","mode":"explore","rationale":"inspect"}`},
			spawn.WithPlannerInferenceScope(scope, "planner-model"),
			spawn.WithPlannerObservationSink(func(_ context.Context, o spawn.PlanObservation) { plan = o; plans++ }),
		)
		spawnTool := spawn.New(child,
			spawn.WithSpawnPlanner(planner, []string{"reader"}),
			spawn.WithAgentResolver(func(string) (*runner.Runner, error) { return child, nil }),
		)
		parentUsage := llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{TotalTokens: 2}}
		parentClient := runnertest.NewClient([][]llm.CompletionChunk{
			{runnertest.ChunkToolCall("spawn", "agent_spawn", `{"prompt":"inspect"}`), parentUsage},
			{runnertest.ChunkText("parent completed"), parentUsage},
		})
		parent := runner.New(parentClient, runner.WithTools(tools.NewRegistry(spawnTool)), runner.WithInferenceScope(scope))
		result := parent.Run(t.Context(), runner.TaskSpec{ID: "root", Prompt: "delegate"})
		if result.Err != nil || result.TotalUsage == nil || result.TotalUsage.TotalTokens != 4 || parentClient.CallCount() != 2 || childClient.CallCount() != 4 {
			t.Fatalf("runner contract changed: %+v, parent calls=%d, child calls=%d", result, parentClient.CallCount(), childClient.CallCount())
		}
		if verdicts != 1 || plans != 1 || !verdict.Valid || !plan.Valid || verdict.Inference.Outcome != inference.StreamOutcomes.COMPLETED || plan.Inference.Outcome != inference.StreamOutcomes.COMPLETED {
			t.Fatalf("helper observations: judge=%+v (%d), planner=%+v (%d)", verdict, verdicts, plan, plans)
		}
		if plan.Inference.TaskID != "root" || plan.Inference.Depth != 0 || verdict.Inference.TaskID == "root" || verdict.Inference.TaskID == "" || verdict.Inference.Depth != 1 {
			t.Fatalf("runner attribution: judge=%+v, planner=%+v", verdict.Inference, plan.Inference)
		}
		if enabled {
			if got := account.Snapshot(); got.Admitted != 8 || got.Settled != 8 || got.Active != 0 || got.Tokens != 30 {
				t.Fatalf("aggregate changed: %+v", got)
			}
		}
	}
}
