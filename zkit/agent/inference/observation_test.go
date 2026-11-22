package inference_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestSettlementSidecarPresence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chunk llm.CompletionChunk
	}{
		{name: "absent"},
		{name: "legacy-zero", chunk: llm.CompletionChunk{UsageReported: true}},
		{name: "unknown", chunk: llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}},
		{name: "known-zero", chunk: llm.CompletionChunk{
			PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}},
			Execution:    &llm.ExecutionObservation{RetryCount: llm.ObservedCount{Known: true}, ReportedCost: llm.ReportedCost{Reported: true}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []inference.Settlement
			account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { events = append(events, s) }))
			scope := inference.Scope{Account: account}
			scope.Wrap(t.Context(), inference.Call{}, func(yield func(llm.CompletionChunk, error) bool) {
				yield(tc.chunk, nil)
			})(func(llm.CompletionChunk, error) bool { return true })
			if len(events) != 1 || !reflect.DeepEqual(events[0].PricingUsage, tc.chunk.PricingUsage) || !reflect.DeepEqual(events[0].Execution, tc.chunk.Execution) || events[0].UsageReported != tc.chunk.UsageReported {
				t.Fatalf("events = %+v", events)
			}
			unknown := 1
			if tc.chunk.UsageReported {
				unknown = 0
			}
			if got := account.Snapshot(); got.Admitted != 1 || got.Settled != 1 || got.Active != 0 || got.Unknown != unknown || got.Tokens != 0 || got.Usage != (llm.Usage{}) {
				t.Fatalf("sidecars changed legacy aggregate: %+v", got)
			}
		})
	}
}

func TestSettlementSidecarsOwnedBeforeDownstreamAndProviderMutation(t *testing.T) {
	var events []inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { events = append(events, s) }))
	scope := inference.Scope{Account: account}
	pricing := llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 2, Known: true}}}}}
	execution := llm.ExecutionObservation{GatewayDeployment: "early-placement"}
	failure := errors.New("provider terminal")
	scope.Wrap(t.Context(), inference.Call{}, func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{PricingUsage: &pricing, Execution: &execution}, nil) {
			return
		}
		pricing.Input.WritesByLifetime[0].Quantity.Tokens = 5
		if !yield(llm.CompletionChunk{PricingUsage: &pricing, UsageReported: true, Usage: llm.Usage{TotalTokens: 3}}, nil) {
			return
		}
		pricing.Input.WritesByLifetime[0].Quantity.Tokens = 100
		execution.GatewayDeployment = "provider-mutation"
		if yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{TotalTokens: 7}}, nil) {
			yield(llm.CompletionChunk{}, failure)
		}
	})(func(chunk llm.CompletionChunk, err error) bool {
		if chunk.PricingUsage != nil {
			chunk.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999
		}
		if chunk.Execution != nil {
			chunk.Execution.GatewayDeployment = "consumer-mutation"
		}
		return err == nil
	})
	if len(events) != 1 {
		t.Fatalf("settlements = %d", len(events))
	}
	got := events[0]
	if !errors.Is(got.Err, failure) || got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 5 || got.Execution.GatewayDeployment != "early-placement" || got.Usage.TotalTokens != 7 {
		t.Fatalf("settled snapshots = %+v / %+v / %+v", got, got.PricingUsage, got.Execution)
	}
	got.Usage.TotalTokens = 500
	got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 500
	got.Execution.GatewayDeployment = "sink-mutation"
	if account.Snapshot().Tokens != 7 || account.Snapshot().Usage.TotalTokens != 7 || pricing.Input.WritesByLifetime[0].Quantity.Tokens != 100 || execution.GatewayDeployment != "provider-mutation" {
		t.Fatal("settlement aliases account aggregate or provider data")
	}
	// A later invocation has its own record, with no inherited sidecars.
	scope.Wrap(t.Context(), inference.Call{}, func(func(llm.CompletionChunk, error) bool) {})(func(llm.CompletionChunk, error) bool { return true })
	if len(events) != 2 || events[1].PricingUsage != nil || events[1].Execution != nil || account.Snapshot().Unknown != 1 {
		t.Fatal("observations leaked across invocations")
	}
}

func TestSidecarSettlementStopErrorPanicAndRefusal(t *testing.T) {
	failure := errors.New("stream terminal")
	for _, kind := range []string{"stop", "error", "panic"} {
		t.Run(kind, func(t *testing.T) {
			var events []inference.Settlement
			account := inference.New(inference.WithLimits(inference.Limits{Invocations: 1}), inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { events = append(events, s) }))
			scope := inference.Scope{Account: account}
			invoke := func() {
				scope.Wrap(t.Context(), inference.Call{}, func(yield func(llm.CompletionChunk, error) bool) {
					if !yield(llm.CompletionChunk{Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true}}}, nil) {
						return
					}
					if kind == "panic" {
						panic("provider panic")
					}
					yield(llm.CompletionChunk{}, failure)
				})(func(_ llm.CompletionChunk, err error) bool { return kind != "stop" && err == nil })
			}
			if kind == "panic" {
				func() {
					defer func() {
						if recover() == nil {
							t.Error("panic swallowed")
						}
					}()
					invoke()
				}()
			} else {
				invoke()
			}
			if len(events) != 1 || events[0].Execution == nil || !events[0].Execution.ReportedCost.Reported || events[0].UsageReported || (kind == "panic") != events[0].Interrupted || !events[0].Stopped && kind == "stop" || kind == "error" && !errors.Is(events[0].Err, failure) {
				t.Fatalf("events = %+v", events)
			}
			var denied error
			scope.Wrap(t.Context(), inference.Call{}, func(func(llm.CompletionChunk, error) bool) { t.Fatal("refused invocation ran") })(func(_ llm.CompletionChunk, err error) bool { denied = err; return false })
			if !errors.Is(denied, inference.ErrBudget) || len(events) != 1 {
				t.Fatalf("refusal = %v, events = %+v", denied, events)
			}
			if got := account.Snapshot(); got.Admitted != 1 || got.Settled != 1 || got.Active != 0 || got.Unknown != 1 || got.Denied != 1 || got.Tokens != 0 {
				t.Fatalf("snapshot = %+v", got)
			}
		})
	}
}

func TestSettlementLatestExecutionSnapshotReplacesRatherThanMerges(t *testing.T) {
	var event inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { event = s }))
	scope := inference.Scope{Account: account}
	scope.Wrap(t.Context(), inference.Call{}, func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{Execution: &llm.ExecutionObservation{GatewayDeployment: "first", ReportedCost: llm.ReportedCost{Amount: 2, Reported: true}}}, nil) {
			return
		}
		if !yield(llm.CompletionChunk{Execution: &llm.ExecutionObservation{}}, nil) {
			return
		}
		yield(llm.CompletionChunk{UsageReported: true}, nil)
	})(func(llm.CompletionChunk, error) bool { return true })
	if !reflect.DeepEqual(event.Execution, &llm.ExecutionObservation{}) || !event.UsageReported || event.PricingUsage != nil {
		t.Fatalf("latest snapshot = %+v", event)
	}
}

// cancellationObservationProvider lends sidecars until yield returns and then
// reuses their storage. Cancellation is supplied by the downstream caller.
type cancellationObservationProvider struct {
	calls     int
	pricing   llm.PricingUsage
	execution llm.ExecutionObservation
}

func (p *cancellationObservationProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.calls++
		defer func() {
			p.pricing.Input.WritesByLifetime[0].Quantity.Tokens = 999
			p.execution.GatewayDeployment = "provider-reuse"
		}()
		if !yield(llm.CompletionChunk{PricingUsage: &p.pricing, Execution: &p.execution}, nil) {
			return
		}
		if cause := context.Cause(ctx); cause != nil {
			yield(llm.CompletionChunk{}, cause)
		}
	}
}

func TestSettlementSidecarsCallerCancellation(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "provider-terminal"
		if stop {
			name = "consumer-stop"
		}
		t.Run(name, func(t *testing.T) {
			var events []inference.Settlement
			account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) {
				events = append(events, s)
			}))
			scope := inference.Scope{Account: account, Member: "cancelled-worker"}
			provider := &cancellationObservationProvider{}
			call := inference.Call{Operation: "runner", Provider: "fixture", Model: "alias"}
			cause := errors.New("caller stopped observation")
			for i := range 2 {
				ctx, cancel := context.WithCancelCause(t.Context())
				t.Cleanup(func() { cancel(nil) })
				provider.pricing = llm.PricingUsage{
					Input: llm.PricingInputUsage{
						Total:            llm.TokenQuantity{Tokens: 13, Known: true},
						WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: int64(i + 1), Known: true}}},
					},
					Output:                    llm.PricingOutputUsage{Total: llm.TokenQuantity{Tokens: 5, Known: true}},
					AdditionalChargesPossible: true,
				}
				provider.execution = llm.ExecutionObservation{GatewayDeployment: "early-placement", ReportedCost: llm.ReportedCost{Reported: true}}
				wantPricing, wantExecution := provider.pricing.Clone(), provider.execution.Clone()
				yields := 0
				var terminal error
				scope.Complete(ctx, call, provider, llm.CompletionRequest{})(func(chunk llm.CompletionChunk, err error) bool {
					yields++
					if err != nil {
						terminal = err
						return false
					}
					if got := account.Snapshot(); got.Admitted != i+1 || got.Settled != i || got.Active != 1 {
						t.Fatalf("in-flight accounting = %+v", got)
					}
					chunk.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 800
					chunk.Execution.GatewayDeployment = "downstream-mutation"
					cancel(cause)
					return !stop
				})
				wantYields := 2
				wantTerminal := cause
				if stop {
					wantYields, wantTerminal = 1, nil
				}
				if yields != wantYields || !errors.Is(terminal, wantTerminal) || provider.calls != i+1 || len(events) != i+1 {
					t.Fatalf("yields=%d terminal=%v calls=%d settlements=%d", yields, terminal, provider.calls, len(events))
				}
				got := events[i]
				if !errors.Is(got.Err, cause) || got.Interrupted || !got.Stopped || got.Call != call || got.Member != scope.Member || got.UsageReported || got.Usage != (llm.Usage{}) || !reflect.DeepEqual(got.PricingUsage, &wantPricing) || !reflect.DeepEqual(got.Execution, &wantExecution) {
					t.Fatalf("cancelled settlement = %+v; pricing=%+v execution=%+v", got, got.PricingUsage, got.Execution)
				}
				if i > 0 && events[0].ID == got.ID {
					t.Fatal("admitted invocations share a settlement ID")
				}
				if provider.pricing.Input.WritesByLifetime[0].Quantity.Tokens != 999 || provider.execution.GatewayDeployment != "provider-reuse" {
					t.Fatal("provider did not reuse borrowed evidence")
				}
				if snapshot := account.Snapshot(); snapshot != (inference.Snapshot{Admitted: i + 1, Settled: i + 1, Unknown: i + 1}) {
					t.Fatalf("sidecars booked as legacy tokens or invocation settled twice: %+v", snapshot)
				}
				// Sink-owned evidence can be mutated without touching provider
				// storage or charging the account after settlement.
				got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 700
				got.Execution.GatewayDeployment = "sink-mutation"
				if provider.pricing.Input.WritesByLifetime[0].Quantity.Tokens != 999 || provider.execution.GatewayDeployment != "provider-reuse" || account.Snapshot().Tokens != 0 {
					t.Fatal("settlement aliases provider or aggregate storage")
				}
			}
		})
	}
}

func TestSettlementPricingSnapshotClearsUnknownFields(t *testing.T) {
	var event inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) {
		event = s
	}))
	scope := inference.Scope{Account: account}
	scope.Wrap(t.Context(), inference.Call{}, func(yield func(llm.CompletionChunk, error) bool) {
		initial := llm.CompletionChunk{
			UsageReported: true, Usage: llm.Usage{TotalTokens: 7},
			PricingUsage: &llm.PricingUsage{
				Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 7, Known: true},
					WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 2, Known: true}}}},
				AdditionalChargesPossible: true,
			},
			Execution: &llm.ExecutionObservation{GatewayDeployment: "retained"},
		}
		for range 2 {
			if !yield(initial, nil) {
				return
			}
		}
		if yield(llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}}, nil) {
			yield(llm.CompletionChunk{}, nil)
		}
	})(func(llm.CompletionChunk, error) bool { return true })
	if !reflect.DeepEqual(event.PricingUsage, &llm.PricingUsage{}) || event.Execution.GatewayDeployment != "retained" || !event.UsageReported {
		t.Fatalf("independent whole snapshots = %+v", event)
	}
	if got := account.Snapshot(); got.Settled != 1 || got.Tokens != 7 || got.Usage.TotalTokens != 7 {
		t.Fatalf("repeated cumulative frames charged more than once: %+v", got)
	}
}
