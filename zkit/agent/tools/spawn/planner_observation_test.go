package spawn_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type observationProvider struct {
	complete func(context.Context, llm.CompletionRequest) llm.CompletionStream
}

func (p observationProvider) Name() string { return "observation-provider" }
func (p observationProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return p.complete(ctx, req)
}

func TestPlanObservationValidityAndPartialFailure(t *testing.T) {
	failure := errors.New("terminal failure")
	for _, tc := range []struct {
		name, body string
		streamErr  error
		valid      bool
	}{
		{name: "valid", body: `{"agent":"reader","mode":"explore","rationale":"inspect"}`, valid: true},
		{name: "malformed", body: "not JSON"},
		{name: "empty"},
		{name: "invalid", body: `{"agent":"invented","mode":"explore"}`},
		{name: "partial-error", body: `{"agent":"reader","mode":"explore","rationale":"inspect"}`, streamErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				account := inference.New()
				scope := inference.Scope{}
				if enabled {
					scope.Account = account
				}
				providerCalls := 0
				provider := observationProvider{complete: func(context.Context, llm.CompletionRequest) llm.CompletionStream {
					providerCalls++
					return func(yield func(llm.CompletionChunk, error) bool) {
						if !yield(llm.CompletionChunk{Content: tc.body, UsageReported: true, Usage: llm.Usage{TotalTokens: 7}, PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}, nil) {
							return
						}
						if tc.streamErr != nil {
							yield(llm.CompletionChunk{}, tc.streamErr)
						}
					}
				}}
				count := 0
				var event spawn.PlanObservation
				helper := spawn.NewLLMSpawnPlanner(provider,
					spawn.WithPlannerInferenceScope(scope, "helper-model"),
					spawn.WithPlannerObservationSink(func(_ context.Context, o spawn.PlanObservation) { event = o; count++ }),
				)
				_, err := helper.Plan(t.Context(), spawn.SpawnPlanInput{AvailableAgents: []spawn.AgentCandidate{{Name: "reader"}}})
				if (err == nil) != tc.valid || !errors.Is(err, tc.streamErr) && tc.streamErr != nil {
					t.Fatalf("response error = %v, valid=%v", err, tc.valid)
				}
				want := inference.StreamOutcomes.COMPLETED
				if tc.streamErr != nil {
					want = inference.StreamOutcomes.FAILED
				}
				if count != 1 || providerCalls != 1 || event.Valid != tc.valid || event.Inference.Outcome != want || event.Inference.Model != "helper-model" || event.Inference.Provider != "observation-provider" || event.Inference.Usage.TotalTokens != 7 || event.Inference.PricingUsage == nil || event.Inference.Execution == nil {
					t.Fatalf("observation = %+v, callbacks=%d, provider calls=%d", event, count, providerCalls)
				}
				clone := event.Clone()
				clone.Inference.Usage.TotalTokens = 100
				clone.Inference.Execution.GatewayDeployment = "mutated"
				if event.Inference.Usage.TotalTokens != 7 || event.Inference.Execution.GatewayDeployment != "" {
					t.Fatal("Clone aliases evidence")
				}
				if enabled {
					if got := account.Snapshot(); got.Settled != 1 || got.Tokens != 7 || got.Active != 0 {
						t.Fatalf("account = %+v", got)
					}
				}
			}
		})
	}
}

func TestPlanObservationAdmissionDenied(t *testing.T) {
	account := inference.New(inference.WithLimits(inference.Limits{Invocations: 1}))
	providerCalls, observations := 0, 0
	provider := observationProvider{complete: func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		providerCalls++
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: `{"agent":"reader","mode":"explore","rationale":"inspect"}`}, nil)
		}
	}}
	helper := spawn.NewLLMSpawnPlanner(provider, spawn.WithPlannerInferenceScope(inference.Scope{Account: account}, "model"), spawn.WithPlannerObservationSink(func(context.Context, spawn.PlanObservation) { observations++ }))
	if _, err := helper.Plan(t.Context(), spawn.SpawnPlanInput{AvailableAgents: []spawn.AgentCandidate{{Name: "reader"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := helper.Plan(t.Context(), spawn.SpawnPlanInput{AvailableAgents: []spawn.AgentCandidate{{Name: "reader"}}}); !errors.Is(err, inference.ErrBudget) {
		t.Fatalf("admission error = %v", err)
	}
	if providerCalls != 1 || observations != 1 {
		t.Fatalf("calls=%d, observations=%d", providerCalls, observations)
	}
}

func TestPlanObservationConcurrentTaskAttribution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		provider := observationProvider{complete: func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
			return func(yield func(llm.CompletionChunk, error) bool) {
				entered <- struct{}{}
				<-release
				body := `{"agent":"reader","mode":"explore","rationale":"inspect"}`
				if taskscope.IDFrom(ctx) == "invalid" {
					body = `{"agent":"invented","mode":"explore"}`
				}
				yield(llm.CompletionChunk{Content: body, UsageReported: true, Usage: llm.Usage{TotalTokens: taskscope.DepthFrom(ctx)}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 3, Known: true}}}}}}, nil)
			}
		}}
		var mu sync.Mutex
		events := map[taskscope.ID]spawn.PlanObservation{}
		helper := spawn.NewLLMSpawnPlanner(provider, spawn.WithPlannerObservationSink(func(_ context.Context, o spawn.PlanObservation) {
			mu.Lock()
			defer mu.Unlock()
			if _, exists := events[o.Inference.TaskID]; exists {
				t.Errorf("duplicate task %s", o.Inference.TaskID)
			}
			events[o.Inference.TaskID] = o.Clone()
			o.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 99
		}))
		var wg sync.WaitGroup
		for i, id := range []taskscope.ID{"valid", "invalid"} {
			wg.Go(func() {
				ctx := taskscope.WithDepth(taskscope.WithID(t.Context(), id), i+1)
				_, err := helper.Plan(ctx, spawn.SpawnPlanInput{AvailableAgents: []spawn.AgentCandidate{{Name: "reader"}}})
				if (err == nil) != (id == "valid") {
					t.Errorf("task %s error = %v", id, err)
				}
			})
		}
		<-entered
		<-entered
		synctest.Wait()
		close(release)
		wg.Wait()
		if len(events) != 2 {
			t.Fatalf("events = %+v", events)
		}
		for i, id := range []taskscope.ID{"valid", "invalid"} {
			o := events[id]
			if o.Inference.TaskID != id || o.Inference.Depth != i+1 || o.Valid != (id == "valid") || o.Inference.Outcome != inference.StreamOutcomes.COMPLETED || o.Inference.Usage.TotalTokens != i+1 || o.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 3 {
				t.Errorf("task %s observation = %+v", id, o)
			}
		}
	})
}

func TestPlannerObservationEmptyCandidates(t *testing.T) {
	calls, observations := 0, 0
	provider := observationProvider{complete: func(context.Context, llm.CompletionRequest) llm.CompletionStream { calls++; return nil }}
	helper := spawn.NewLLMSpawnPlanner(provider, spawn.WithPlannerObservationSink(func(context.Context, spawn.PlanObservation) { observations++ }))
	if _, err := helper.Plan(t.Context(), spawn.SpawnPlanInput{}); err == nil {
		t.Fatal("empty candidates unexpectedly valid")
	}
	if calls != 0 || observations != 0 {
		t.Fatalf("calls=%d, observations=%d", calls, observations)
	}
}

func TestPlannerObservationAuthoredAgentSkipsInference(t *testing.T) {
	calls, observations := 0, 0
	provider := observationProvider{complete: func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		calls++
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: `{"agent":"reader","mode":"explore"}`}, nil)
		}
	}}
	planner := spawn.NewLLMSpawnPlanner(provider, spawn.WithPlannerObservationSink(func(context.Context, spawn.PlanObservation) { observations++ }))
	child := runner.New(runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText("done")}}))
	tool := spawn.New(child, spawn.WithSpawnPlanner(planner, []string{"reader"}), spawn.WithAgentResolver(func(string) (*runner.Runner, error) { return child, nil }))
	result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "authored", Arguments: tools.ToolParameters{"prompt": "inspect", "agent": "reader"}})
	if err != nil || !result.Success || calls != 0 || observations != 0 {
		t.Fatalf("authored routing: result=%+v, error=%v, calls=%d, observations=%d", result, err, calls, observations)
	}
}
