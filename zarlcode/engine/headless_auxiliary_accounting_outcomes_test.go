package engine_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestHeadlessAuxiliaryAccountingStreamOutcomesAndSafeErrors(t *testing.T) {
	for _, outcome := range []string{"malformed", "empty", "failed", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			settings := headlessAuxiliarySettings(t)
			p := &auxiliaryOutcomeProvider{outcome: outcome, cause: errors.New("https://private-error-canary.invalid/token")}
			live := recordedAuxiliaryLive(t, settings, p)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := live.RunHeadlessWithObservations(ctx, "trigger repeated signature", 6)
			rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
			if len(rows) != 1 {
				t.Fatalf("aux=%d", len(rows))
			}
			got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(rows[0].PayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			want := inference.StreamOutcomes.COMPLETED
			if outcome == "failed" {
				want = inference.StreamOutcomes.FAILED
			}
			if outcome == "cancelled" {
				want = inference.StreamOutcomes.CANCELLED
			}
			if got.StreamOutcome != want || got.Usage.PromptTokens != 1000 || got.ReportedCost == nil || got.ReportedCost.Source != "" {
				t.Fatalf("outcome %+v", got)
			}
			if bytes.Contains(rows[0].PayloadJSON, []byte("canary")) {
				t.Fatal("raw error/response retained")
			}
		})
	}
}

func TestHeadlessAuxiliaryAccountingLatestIndependentEvidencePresence(t *testing.T) {
	for _, presence := range []string{"absent", "zero", "latest"} {
		t.Run(presence, func(t *testing.T) {
			settings := headlessAuxiliarySettings(t)
			base := &auxiliaryRunProvider{}
			provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
				if req.ResponseFormat.Name != "decompose_verdict" {
					return base.Complete(ctx, req)
				}
				return func(yield func(llm.CompletionChunk, error) bool) {
					if presence == "latest" {
						e := auxiliaryEvidence()
						if !yield(llm.CompletionChunk{UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil) {
							return
						}
						e.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999999
						e.Execution.ReportedCost.Amount = 999999
					}
					if presence != "absent" {
						if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}}, Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true}}}, nil) {
							return
						}
					}
					// Nil final sidecars retain the independently latest non-nil evidence.
					yield(llm.CompletionChunk{Content: `{"action":"smaller_scope","rationale":"fixture"}`}, nil)
				}
			})
			live := recordedAuxiliaryLive(t, settings, provider)
			result := live.RunHeadlessWithObservations(t.Context(), "latest evidence", 6)
			rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
			if len(rows) != 1 {
				t.Fatal("missing helper")
			}
			got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(rows[0].PayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			if got.RateCard == nil || (presence == "absent" && got.ConfiguredEstimate != nil) {
				t.Fatal("bound card/absent usage price semantics changed")
			}
			if presence == "absent" {
				if got.Usage != nil || got.PricingUsage != nil || got.ReportedCost != nil {
					t.Fatal("absence became zero")
				}
			} else if got.Usage == nil || got.Usage.TotalTokens != 0 || got.PricingUsage == nil || !got.PricingUsage.Input.Total.Known || got.PricingUsage.Input.Total.Tokens != 0 || len(got.PricingUsage.Input.WritesByLifetime) != 0 || got.ReportedCost == nil || !got.ReportedCost.Reported || got.ReportedCost.Amount != 0 {
				t.Fatalf("latest/zero not retained: %+v", got)
			}
		})
	}
}

func TestHeadlessAuxiliaryAccountingCompactorTerminalOutcomes(t *testing.T) {
	for _, kind := range []string{"summary", "executive", "handover"} {
		for _, outcome := range []string{"empty", "failed", "cancelled", "cause", "panic"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: kind, prefs.KeyCompactionMode: "auto", prefs.KeySpawnEnabled: "off"})
				roots, calls := 0, 0
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				cause := errors.New("synthetic custom cancellation cause canary")
				provider := recordedAccountingProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
					if len(req.Tools) == 0 {
						return func(yield func(llm.CompletionChunk, error) bool) {
							calls++
							e := auxiliaryEvidence()
							if !yield(llm.CompletionChunk{UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil) {
								return
							}
							if outcome == "failed" {
								yield(llm.CompletionChunk{}, errors.New("synthetic private compactor failure canary"))
							}
							if outcome == "cancelled" {
								yield(llm.CompletionChunk{}, context.DeadlineExceeded)
							}
							if outcome == "cause" {
								cancel(cause)
								yield(llm.CompletionChunk{}, context.Cause(ctx))
							}
							if outcome == "panic" {
								panic(cause)
							}
						}
					}
					roots++
					return func(yield func(llm.CompletionChunk, error) bool) {
						yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 2000, TotalTokens: 2000}, ToolCalls: []llm.ToolCall{{ID: "read-" + string(rune('a'+roots)), Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"synthetic-missing"}`}}}}, nil)
					}
				})
				live := recordedAuxiliaryLive(t, settings, provider)
				var contextCache []llm.Message
				for range 20 {
					contextCache = append(contextCache, llm.Message{Role: llm.RoleUser, Content: "older evidence"}, llm.Message{Role: llm.RoleAssistant, Content: "older response"})
				}
				live.RestoreContext(contextCache)
				result := live.RunHeadlessWithObservations(ctx, "compactor terminal stream", 6)
				if outcome == "panic" && result.Task.Err == nil {
					t.Fatal("headless panic was not reported")
				}

				if outcome == "cause" && !errors.Is(context.Cause(ctx), cause) {
					t.Fatal("custom cause lost")
				}
				rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
				if len(rows) == 0 || len(rows) != calls || ((outcome == "panic" || outcome == "cause") && calls != 1) {
					t.Fatalf("compactor invocations=%d captured=%d", calls, len(rows))
				}
				want := inference.StreamOutcomes.COMPLETED
				if outcome == "failed" {
					want = inference.StreamOutcomes.FAILED
				}
				if outcome == "cancelled" || outcome == "cause" {
					want = inference.StreamOutcomes.CANCELLED
				}
				if outcome == "panic" {
					want = inference.StreamOutcomes.INTERRUPTED
				}
				for _, row := range rows {
					got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(row.PayloadJSON)
					if err != nil || got.StreamOutcome != want || got.Usage == nil || *got.Usage != *auxiliaryEvidence().Usage || got.ReportedCost == nil || got.ReportedCost.Source != "" {
						t.Fatalf("compactor evidence %+v %v", got, err)
					}
				}
			})
		}
	}
}

func TestHeadlessAuxiliaryAccountingPlannerTerminalOutcomes(t *testing.T) {
	for _, outcome := range []string{"malformed", "empty", "failed", "cancelled", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			settings := headlessAuxiliarySettings(t)
			base := &auxiliaryRunProvider{recurse: true}
			calls := 0
			provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
				if req.ResponseFormat.Name != "spawn_plan" {
					return base.Complete(ctx, req)
				}
				return func(yield func(llm.CompletionChunk, error) bool) {
					calls++
					e := auxiliaryEvidence()
					content := "private malformed planner response canary"
					if outcome == "empty" {
						content = ""
					}
					if !yield(llm.CompletionChunk{Content: content, UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil) {
						return
					}
					switch outcome {
					case "failed":
						yield(llm.CompletionChunk{}, errors.New("private planner failure canary"))
					case "cancelled":
						yield(llm.CompletionChunk{}, context.Canceled)
					case "panic":
						panic("synthetic planner panic")
					}
				}
			})
			live := recordedAuxiliaryLive(t, settings, provider)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := live.RunHeadlessWithObservations(ctx, "planner runtime outcomes", 6)
			rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
			want := inference.StreamOutcomes.COMPLETED
			switch outcome {
			case "failed":
				want = inference.StreamOutcomes.FAILED
			case "cancelled":
				want = inference.StreamOutcomes.CANCELLED
			case "panic":
				want = inference.StreamOutcomes.INTERRUPTED
			}
			captured := 0
			for _, row := range rows {
				got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(row.PayloadJSON)
				if err != nil {
					t.Fatal(err)
				}
				if got.Purpose != engine.AuxiliaryAccountingPurposes.PURPOSESPAWNPLANNER {
					continue
				}
				captured++
				if got.StreamOutcome != want || got.Usage.PromptTokens != 1000 || got.ReportedCost == nil {
					t.Fatalf("planner evidence %+v", got)
				}
			}
			if calls == 0 || captured != calls {
				t.Fatalf("calls=%d captured=%d", calls, captured)
			}
		})
	}
}
