package inference_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type usageClient struct {
	calls   int
	content string
}

func (p *usageClient) Name() string { return "fixture" }
func (p *usageClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	p.calls++
	return func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{Content: p.content}, nil) {
			return
		}
		yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7}}, nil)
	}
}

func TestAuxiliaryInferenceSharesBudgetWithRunner(t *testing.T) {
	account := inference.New(inference.WithLimits(inference.Limits{Invocations: 4}))
	scope := inference.Scope{Account: account, Member: "worker"}
	plannerProvider := &usageClient{content: `{"agent":"","mode":"explore","rationale":"inspect"}`}
	planner := spawn.NewLLMSpawnPlanner(plannerProvider, spawn.WithPlannerInferenceScope(scope, "planner-model"))
	if _, err := planner.Plan(t.Context(), spawn.SpawnPlanInput{Prompt: "inspect", AvailableAgents: []spawn.AgentCandidate{{Name: "reader"}}}); err != nil {
		t.Fatal(err)
	}
	judgeProvider := &usageClient{content: `{"action":"smaller_scope","rationale":"retry narrowly"}`}
	judge := guardrails.NewLLMVerdictJudge(judgeProvider, guardrails.WithVerdictInferenceScope(scope, "judge-model"))
	if _, err := judge.Judge(t.Context(), guardrails.VerdictInput{}); err != nil {
		t.Fatal(err)
	}
	summarizer := &compact.Summary{Provider: &usageClient{content: "Retained investigation context."}, InferenceScope: scope}
	if _, err := summarizer.Compact(t.Context(), []llm.Message{{Role: llm.RoleUser, Content: "old"}, {Role: llm.RoleAssistant, Content: "history"}, {Role: llm.RoleUser, Content: "recent"}}, 1); err != nil {
		t.Fatal(err)
	}
	direct := &usageClient{content: "Finished inspection."}
	r := runner.New(direct, runner.WithInferenceScope(scope))
	result := r.Run(t.Context(), runner.TaskSpec{ID: "first", Prompt: "inspect"})
	if result.Err != nil || result.TotalUsage == nil || result.TotalUsage.TotalTokens != 7 {
		t.Fatalf("task totals changed: %+v", result)
	}
	denied := r.Run(t.Context(), runner.TaskSpec{ID: "second", Prompt: "inspect"})
	if !errors.Is(denied.Err, inference.ErrBudget) || denied.TotalUsage != nil || denied.Timing.ProviderAttempts != 0 || direct.calls != 1 {
		t.Fatalf("denied invocation charged/executed: %+v calls=%d", denied, direct.calls)
	}
	got := account.Snapshot()
	if got.Admitted != 4 || got.Tokens != 28 || got.Active != 0 || got.Denied != 1 {
		t.Fatalf("aggregate=%+v", got)
	}
}

func TestRunnerNormalCompletionSettlesWithoutCancellation(t *testing.T) {
	var event inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, settlement inference.Settlement) {
		event = settlement
	}))
	provider := &usageClient{content: "Finished inspection."}
	r := runner.New(provider, runner.WithInferenceScope(inference.Scope{Account: account}))

	result := r.Run(t.Context(), runner.TaskSpec{ID: "normal", Prompt: "inspect"})
	if result.Err != nil {
		t.Fatalf("run error = %v", result.Err)
	}
	if event.Err != nil || event.Interrupted {
		t.Fatalf("settlement = %+v", event)
	}
	if got := account.Snapshot(); got.Admitted != 1 || got.Settled != 1 || got.Active != 0 {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestAuxiliaryParseFailureRetainsUsage(t *testing.T) {
	account := inference.New()
	scope := inference.Scope{Account: account}
	provider := &usageClient{content: "not valid JSON"}
	planner := spawn.NewLLMSpawnPlanner(provider, spawn.WithPlannerInferenceScope(scope, ""))
	if _, err := planner.Plan(t.Context(), spawn.SpawnPlanInput{Prompt: "work", AvailableAgents: []spawn.AgentCandidate{{Name: "worker"}}}); err == nil {
		t.Fatal("expected parse failure")
	}
	if got := account.Snapshot(); got.Tokens != 7 || got.Settled != 1 {
		t.Fatalf("parse failure erased consumption: %+v", got)
	}
}
