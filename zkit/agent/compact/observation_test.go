package compact_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type observationProvider struct {
	stream llm.CompletionStream
	calls  int
}

func (*observationProvider) Name() string { return "auxiliary-provider" }
func (p *observationProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	p.calls++
	return p.stream
}

func observationCompactor(engine string, p llm.Provider, model string, scope inference.Scope) compact.Compactor {
	switch engine {
	case compact.EngineSummary:
		c := compact.NewSummary(p, model)
		c.InferenceScope = scope
		return c
	case compact.EngineExecutive:
		c := compact.NewExecutive(p, model, nil)
		c.InferenceScope = scope
		return c
	default:
		c := compact.NewHandover(p, model, nil, nil)
		c.InferenceScope = scope
		return c
	}
}

func observationHistory() []llm.Message {
	return []llm.Message{{Role: llm.RoleUser, Content: "old"}, {Role: llm.RoleAssistant, Content: "answer"}, {Role: llm.RoleUser, Content: "recent"}}
}

func TestCompactionObservationPresence(t *testing.T) {
	for _, engine := range []string{compact.EngineSummary, compact.EngineExecutive, compact.EngineHandover} {
		for _, accounted := range []bool{false, true} {
			for _, tc := range []struct {
				name  string
				chunk llm.CompletionChunk
			}{
				{name: "absent"},
				{name: "legacy-only", chunk: llm.CompletionChunk{UsageReported: true}},
				{name: "present-unknown", chunk: llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}},
				{name: "known-zero", chunk: llm.CompletionChunk{
					PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}, WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: time.Hour, Quantity: llm.TokenQuantity{Known: true}}}}},
					Execution:    &llm.ExecutionObservation{RetryCount: llm.ObservedCount{Known: true}, ReportedCost: llm.ReportedCost{Reported: true}},
				}},
			} {
				name := engine + "/account-free/" + tc.name
				if accounted {
					name = engine + "/accounted/" + tc.name
				}
				t.Run(name, func(t *testing.T) {
					scope := inference.Scope{}
					if accounted {
						scope.Account = inference.New()
					}
					p := &observationProvider{stream: func(yield func(llm.CompletionChunk, error) bool) {
						if yield(tc.chunk, nil) {
							yield(llm.CompletionChunk{Content: "summary"}, nil)
						}
					}}
					// An unspecified label stays unknown, never inferred from the provider.
					result, err := observationCompactor(engine, p, "", scope).Compact(t.Context(), observationHistory(), 1)
					if err != nil || result.Inference == nil || p.calls != 1 {
						t.Fatalf("Compact = %+v, %v; Complete calls = %d", result, err, p.calls)
					}
					got := result.Inference
					var usage *llm.Usage
					if tc.chunk.UsageReported {
						usage = &tc.chunk.Usage
					}
					if got.Provider != p.Name() || got.Model != "" || got.Duration < 0 || !reflect.DeepEqual(got.Usage, usage) || !reflect.DeepEqual(got.PricingUsage, tc.chunk.PricingUsage) || !reflect.DeepEqual(got.Execution, tc.chunk.Execution) {
						t.Fatalf("observation = %+v; chunk = %+v", got, tc.chunk)
					}
				})
			}
		}
	}
}

func TestCompactionLatestIndependentOwnedSnapshots(t *testing.T) {
	failure := errors.New("auxiliary stream failed")
	for _, engine := range []string{compact.EngineSummary, compact.EngineExecutive, compact.EngineHandover} {
		for _, outcome := range []string{"success", "failure", "cancel", "empty"} {
			t.Run(engine+"/"+outcome, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				pricing := llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true, Tokens: 99}, WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Known: true, Tokens: 2}}}}}
				execution := llm.ExecutionObservation{GatewayDeployment: "first", ReportedModel: "old-model"}
				p := &observationProvider{stream: func(yield func(llm.CompletionChunk, error) bool) {
					if !yield(llm.CompletionChunk{PricingUsage: &pricing, Execution: &execution, UsageReported: true, Usage: llm.Usage{PromptTokens: 3}}, nil) {
						return
					}
					// Replace pricing completely: Total must not be merged from the old snapshot.
					pricing.Input.Total = llm.TokenQuantity{}
					pricing.Input.WritesByLifetime[0].Quantity.Tokens = 5
					if !yield(llm.CompletionChunk{PricingUsage: &pricing}, nil) {
						return
					}
					pricing.Input.WritesByLifetime[0].Quantity.Tokens = 999
					execution = llm.ExecutionObservation{GatewayDeployment: "latest"}
					if !yield(llm.CompletionChunk{Execution: &execution, UsageReported: true, Usage: llm.Usage{PromptTokens: 7}}, nil) {
						return
					}
					execution.GatewayDeployment = "borrowed-mutation"
					// Nil sidecars and unreported usage retain prior evidence independently.
					if !yield(llm.CompletionChunk{}, nil) {
						return
					}
					switch outcome {
					case "failure":
						yield(llm.CompletionChunk{}, failure)
					case "cancel":
						cancel(failure)
						yield(llm.CompletionChunk{}, context.Cause(ctx))
					case "success":
						yield(llm.CompletionChunk{Content: "summary"}, nil)
					}
				}}
				result, err := observationCompactor(engine, p, "supplied-label", inference.Scope{}).Compact(ctx, observationHistory(), 1)
				switch {
				case outcome == "failure" || outcome == "cancel":
					if !errors.Is(err, failure) {
						t.Fatalf("error = %v, want cause %v", err, failure)
					}
				case outcome == "empty" && engine != compact.EngineExecutive:
					if err == nil {
						t.Fatal("empty summary should be rejected")
					}
				case err != nil:
					t.Fatal(err)
				}
				got := result.Inference
				if got == nil || got.Provider != p.Name() || got.Model != "supplied-label" || got.Usage == nil || got.PricingUsage == nil || got.Execution == nil {
					t.Fatalf("inference = %+v", got)
				}
				if got.Usage.PromptTokens != 7 || got.PricingUsage.Input.Total.Known || got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 5 || got.Execution.GatewayDeployment != "latest" || got.Execution.ReportedModel != "" {
					t.Fatalf("snapshots = %+v / %+v / %+v", got.Usage, got.PricingUsage, got.Execution)
				}
			})
		}
	}
}

func TestCompactionAdmissionRefusalHasNoInferenceOrComplete(t *testing.T) {
	for _, engine := range []string{compact.EngineSummary, compact.EngineExecutive, compact.EngineHandover} {
		for _, canceled := range []bool{false, true} {
			t.Run(engine+map[bool]string{false: "/budget", true: "/cancel"}[canceled], func(t *testing.T) {
				account := inference.New(inference.WithLimits(inference.Limits{Invocations: 1}))
				scope := inference.Scope{Account: account}
				for range scope.Wrap(t.Context(), inference.Call{}, func(func(llm.CompletionChunk, error) bool) {}) {
				}
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				want := inference.ErrBudget
				if canceled {
					want = context.Canceled
					cancel(want)
				}
				p := &observationProvider{}
				result, err := observationCompactor(engine, p, "label", scope).Compact(ctx, observationHistory(), 1)
				if !errors.Is(err, want) || result.Inference != nil || p.calls != 0 {
					t.Fatalf("Compact = %+v, %v; Complete calls = %d", result, err, p.calls)
				}
			})
		}
	}
}

func TestInferenceCloneIsolation(t *testing.T) {
	original := compact.Inference{Provider: "provider", Model: "label", Duration: time.Second, Usage: &llm.Usage{PromptTokens: 7}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Known: true}}}}}, Execution: &llm.ExecutionObservation{GatewayDeployment: "deployment", ReportedCost: llm.ReportedCost{Reported: true}}}
	clone := original.Clone()
	if !reflect.DeepEqual(original, clone) {
		t.Fatal("Clone changed evidence")
	}
	clone.Usage.PromptTokens = 999
	clone.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999
	clone.Execution.GatewayDeployment = "changed"
	if original.Usage.PromptTokens != 7 || original.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 0 || original.Execution.GatewayDeployment != "deployment" {
		t.Fatal("Clone aliases original")
	}
	for _, o := range []compact.Inference{{}, {Usage: &llm.Usage{}, PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}} {
		if !reflect.DeepEqual(o, o.Clone()) {
			t.Fatal("Clone lost absent or present unknown/zero evidence")
		}
	}
}
