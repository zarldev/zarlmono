package inference_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type observedClient func(context.Context, llm.CompletionRequest) llm.CompletionStream

func (f observedClient) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return f(ctx, req)
}

func drainObserved(stream llm.CompletionStream) {
	stream(func(_ llm.CompletionChunk, err error) bool { return err == nil })
}

func TestInvokeObservedPresenceAndAccountingEquivalence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chunk llm.CompletionChunk
	}{
		{name: "absent"},
		{name: "legacy-zero", chunk: llm.CompletionChunk{UsageReported: true}},
		{name: "unknown", chunk: llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}},
		{name: "known-zero", chunk: llm.CompletionChunk{
			UsageReported: true,
			PricingUsage:  &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}},
			Execution:     &llm.ExecutionObservation{RetryCount: llm.ObservedCount{Known: true}, ReportedCost: llm.ReportedCost{Reported: true}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) { yield(tc.chunk, nil) }
			})
			var zero, accounted inference.Observation
			for _, enabled := range []bool{false, true} {
				account := inference.New()
				scope := inference.Scope{}
				if enabled {
					scope.Account = account
				}
				ctx := taskscope.WithDepth(taskscope.WithID(t.Context(), "child"), 2)
				count := 0
				err := scope.InvokeObserved(ctx, inference.Call{Operation: "custom", Provider: "provider", Model: "model"}, source, llm.CompletionRequest{}, drainObserved, func(_ context.Context, o inference.Observation) {
					count++
					if enabled {
						accounted = o
					} else {
						zero = o
					}
				})
				if err != nil || count != 1 {
					t.Fatalf("InvokeObserved = %v, callbacks = %d", err, count)
				}
				if enabled {
					plain := inference.New()
					if err := (inference.Scope{Account: plain}).Invoke(ctx, inference.Call{}, source, llm.CompletionRequest{}, drainObserved); err != nil {
						t.Fatal(err)
					}
					if account.Snapshot() != plain.Snapshot() {
						t.Fatalf("observed accounting = %+v, plain = %+v", account.Snapshot(), plain.Snapshot())
					}
				}
			}
			zero.Duration, accounted.Duration = 0, 0
			if !reflect.DeepEqual(zero, accounted) || zero.TaskID != "child" || zero.Depth != 2 || zero.Operation != "custom" || zero.Provider != "provider" || zero.Model != "model" || zero.Outcome != inference.StreamOutcomes.COMPLETED {
				t.Fatalf("zero = %+v, accounted = %+v", zero, accounted)
			}
			if (zero.Usage != nil) != tc.chunk.UsageReported || !reflect.DeepEqual(zero.PricingUsage, tc.chunk.PricingUsage) || !reflect.DeepEqual(zero.Execution, tc.chunk.Execution) {
				t.Fatalf("presence lost: %+v", zero)
			}
		})
	}
}

func TestInvokeObservedLatestSnapshotsAndOwnership(t *testing.T) {
	pricing := llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 8, Known: true}, WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 2, Known: true}}}}}
	execution := llm.ExecutionObservation{GatewayDeployment: "original"}
	var event inference.Observation
	var settled inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) {
		settled = s
		s.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 900
		s.Execution.GatewayDeployment = "account-mutation"
	}))
	source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 8}, PricingUsage: &pricing, Execution: &execution}, nil) {
				return
			}
			// Whole latest usage, including the unreported reasoning flag, replaces.
			if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{CompletionTokens: 3, ReasoningTokensReported: true}}, nil) {
				return
			}
			pricing.Input.Total = llm.TokenQuantity{}
			pricing.Input.WritesByLifetime[0].Quantity.Tokens = 4
			if !yield(llm.CompletionChunk{PricingUsage: &pricing}, nil) {
				return
			}
			pricing.Input.WritesByLifetime[0].Quantity.Tokens = 800
			execution.GatewayDeployment = "provider-mutation"
			yield(llm.CompletionChunk{}, nil) // Nil sidecars retain the latest independently.
		}
	})
	err := (inference.Scope{Account: account}).InvokeObserved(t.Context(), inference.Call{}, source, llm.CompletionRequest{}, func(stream llm.CompletionStream) {
		stream(func(chunk llm.CompletionChunk, _ error) bool {
			if chunk.PricingUsage != nil {
				chunk.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 700
			}
			if chunk.Execution != nil {
				chunk.Execution.GatewayDeployment = "downstream-mutation"
			}
			return true
		})
	}, func(_ context.Context, o inference.Observation) { event = o })
	if err != nil {
		t.Fatal(err)
	}
	if *event.Usage != (llm.Usage{CompletionTokens: 3, ReasoningTokensReported: true}) || event.PricingUsage.Input.Total.Known || event.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 4 || event.Execution.GatewayDeployment != "original" {
		t.Fatalf("snapshot merged or aliased: %+v, %+v, %+v", event.Usage, event.PricingUsage, event.Execution)
	}
	clone := event.Clone()
	clone.Usage.CompletionTokens = 100
	clone.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 100
	clone.Execution.GatewayDeployment = "clone-mutation"
	if event.Usage.CompletionTokens != 3 || event.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 4 || event.Execution.GatewayDeployment != "original" || settled.Usage.CompletionTokens != 3 {
		t.Fatal("clone/account ownership lost")
	}
}

func TestInvokeObservedOutcomes(t *testing.T) {
	failure := errors.New("terminal")
	customCancel := errors.New("custom cancel cause")
	for _, tc := range []struct {
		name    string
		err     error
		cancel  error
		stop    bool
		cleanup bool
		want    inference.StreamOutcome
	}{
		{name: "EOF", want: inference.StreamOutcomes.COMPLETED},
		{name: "EOF-cleanup-cancel", cleanup: true, want: inference.StreamOutcomes.COMPLETED},
		{name: "partial-failure", err: failure, want: inference.StreamOutcomes.FAILED},
		{name: "failure-cleanup-cancel", err: failure, cleanup: true, want: inference.StreamOutcomes.FAILED},
		{name: "cancel-identity", err: context.Canceled, want: inference.StreamOutcomes.CANCELLED},
		{name: "deadline-identity", err: context.DeadlineExceeded, want: inference.StreamOutcomes.CANCELLED},
		{name: "custom-cause", cancel: customCancel, err: customCancel, want: inference.StreamOutcomes.CANCELLED},
		{name: "stop", stop: true, want: inference.StreamOutcomes.STOPPED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
					return func(yield func(llm.CompletionChunk, error) bool) {
						if tc.cleanup {
							defer cancel(customCancel)
						}
						if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{TotalTokens: 7}}, nil) {
							return
						}
						if tc.cancel != nil {
							cancel(tc.cancel)
						}
						if tc.err != nil {
							yield(llm.CompletionChunk{}, tc.err)
						}
					}
				})
				scope := inference.Scope{}
				if enabled {
					scope.Account = inference.New()
				}
				count := 0
				err := scope.InvokeObserved(ctx, inference.Call{}, source, llm.CompletionRequest{}, func(stream llm.CompletionStream) {
					stream(func(_ llm.CompletionChunk, err error) bool { return !tc.stop && err == nil })
				}, func(_ context.Context, o inference.Observation) {
					count++
					if o.Outcome != tc.want || o.Usage == nil || o.Usage.TotalTokens != 7 {
						t.Errorf("observation = %+v, want %s", o, tc.want)
					}
				})
				if err != nil || count != 1 {
					t.Fatalf("err = %v, callbacks = %d", err, count)
				}
			}
		})
	}
}

func TestInvokeObservedInterruptedAndUnconsumed(t *testing.T) {
	panicValue := struct{ marker string }{"panic sentinel"}
	for _, stage := range []string{"construction", "stream", "downstream", "consume", "unconsumed"} {
		t.Run(stage, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				count, settlements := 0, 0
				scope := inference.Scope{}
				if enabled {
					scope.Account = inference.New(inference.WithSettlementSink(func(context.Context, inference.Settlement) { settlements++ }))
				}
				source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
					if stage == "construction" {
						panic(panicValue)
					}
					return func(yield func(llm.CompletionChunk, error) bool) {
						if stage == "stream" {
							panic(panicValue)
						}
						yield(llm.CompletionChunk{UsageReported: true}, nil)
					}
				})
				func() {
					defer func() {
						got := recover()
						if stage == "unconsumed" {
							if got != nil {
								t.Errorf("unexpected panic: %v", got)
							}
						} else if got != panicValue {
							t.Errorf("panic = %v, want original sentinel", got)
						}
					}()
					if err := scope.InvokeObserved(t.Context(), inference.Call{}, source, llm.CompletionRequest{}, func(stream llm.CompletionStream) {
						if stage == "consume" {
							panic(panicValue)
						}
						if stage == "unconsumed" {
							return
						}
						stream(func(llm.CompletionChunk, error) bool { panic(panicValue) })
					}, func(_ context.Context, o inference.Observation) {
						count++
						if o.Outcome != inference.StreamOutcomes.INTERRUPTED || enabled && settlements != 1 {
							t.Errorf("observation = %+v, settlements = %d", o, settlements)
						}
					}); err != nil {
						t.Error(err)
					}
				}()
				if count != 1 {
					t.Fatalf("callbacks = %d", count)
				}
				if enabled {
					if got := scope.Account.Snapshot(); got.Admitted != 1 || got.Settled != 1 || got.Active != 0 {
						t.Fatalf("snapshot = %+v", got)
					}
				}
			}
		})
	}
}

func TestInvokeObservedDenialAndZeroScopeAdmission(t *testing.T) {
	account := inference.New(inference.WithLimits(inference.Limits{Invocations: 1}))
	scope := inference.Scope{Account: account}
	sourceCalls, observations := 0, 0
	source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		sourceCalls++
		return func(func(llm.CompletionChunk, error) bool) {}
	})
	observe := func(context.Context, inference.Observation) { observations++ }
	if err := scope.InvokeObserved(t.Context(), inference.Call{}, source, llm.CompletionRequest{}, drainObserved, observe); err != nil {
		t.Fatal(err)
	}
	if err := scope.InvokeObserved(t.Context(), inference.Call{}, source, llm.CompletionRequest{}, drainObserved, observe); !errors.Is(err, inference.ErrBudget) {
		t.Fatalf("budget error = %v", err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("cancel admission")
	cancel(cause)
	if err := scope.InvokeObserved(ctx, inference.Call{}, source, llm.CompletionRequest{}, drainObserved, observe); !errors.Is(err, cause) {
		t.Fatalf("cancel error = %v", err)
	}
	if sourceCalls != 1 || observations != 1 || account.Snapshot().Denied != 1 {
		t.Fatalf("source = %d, observed = %d, snapshot = %+v", sourceCalls, observations, account.Snapshot())
	}
	// The zero scope intentionally admits even a canceled context, as Invoke does.
	if err := (inference.Scope{}).InvokeObserved(ctx, inference.Call{}, source, llm.CompletionRequest{}, drainObserved, observe); err != nil || sourceCalls != 2 || observations != 2 {
		t.Fatalf("zero-scope admission changed: %v, calls=%d, observations=%d", err, sourceCalls, observations)
	}
}

func TestInvokeObservedFinalizesBeforeBlockedSettlement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settleEntered, releaseSettle, releaseYield, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		var event inference.Observation
		callbacks := 0
		account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) {
			if s.Usage.TotalTokens != 3 {
				t.Errorf("settlement = %+v", s)
			}
			close(settleEntered)
			<-releaseSettle
		}))
		source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
			return func(yield func(llm.CompletionChunk, error) bool) {
				time.Sleep(time.Second) // Virtual provider duration.
				yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{TotalTokens: 3}}, nil)
			}
		})
		go func() {
			defer close(done)
			if err := (inference.Scope{Account: account}).InvokeObserved(t.Context(), inference.Call{}, source, llm.CompletionRequest{}, func(stream llm.CompletionStream) {
				stream(func(llm.CompletionChunk, error) bool { <-releaseYield; return true })
				time.Sleep(20 * time.Second) // Later consumer work is excluded too.
			}, func(_ context.Context, o inference.Observation) { event = o; callbacks++ }); err != nil {
				t.Error(err)
			}
		}()
		time.Sleep(3 * time.Second)
		close(releaseYield)
		<-settleEntered
		if callbacks != 0 || account.Snapshot().Settled != 1 {
			t.Fatalf("observer ran before settlement callback: %d, %+v", callbacks, account.Snapshot())
		}
		time.Sleep(10 * time.Second)
		close(releaseSettle)
		<-done
		if callbacks != 1 || event.Duration != 3*time.Second || event.Outcome != inference.StreamOutcomes.COMPLETED {
			t.Fatalf("observation = %+v, callbacks=%d", event, callbacks)
		}
	})
}

func TestStreamOutcomeVocabulary(t *testing.T) {
	if (inference.StreamOutcome{}).IsValid() {
		t.Fatal("zero outcome is valid")
	}
	count := 0
	for outcome := range inference.StreamOutcomes.All() {
		parsed, err := inference.ParseStreamOutcome(outcome.String())
		if err != nil || parsed != outcome {
			t.Fatalf("round trip %s = %s, %v", outcome, parsed, err)
		}
		count++
	}
	if _, err := inference.ParseStreamOutcome("made-up"); !errors.Is(err, inference.ErrParseStreamOutcome) || count != 5 {
		t.Fatalf("enum parse = %v, count=%d", err, count)
	}
}

func TestInvokeObservedNonNilUnknownReplacesSidecars(t *testing.T) {
	source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			if !yield(llm.CompletionChunk{
				PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 7, Known: true}, WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Known: true}}}}},
				Execution:    &llm.ExecutionObservation{GatewayDeployment: "first", RetryCount: llm.ObservedCount{Known: true}},
			}, nil) {
				return
			}
			if !yield(llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}, nil) {
				return
			}
			yield(llm.CompletionChunk{}, nil)
		}
	})
	if err := (inference.Scope{}).InvokeObserved(t.Context(), inference.Call{}, source, llm.CompletionRequest{}, drainObserved, func(_ context.Context, o inference.Observation) {
		if o.Usage != nil || !reflect.DeepEqual(o.PricingUsage, &llm.PricingUsage{}) || !reflect.DeepEqual(o.Execution, &llm.ExecutionObservation{}) {
			t.Errorf("non-nil unknown did not replace: %+v", o)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInvokeObservedFallbackFinalizesBeforeBlockedSettlement(t *testing.T) {
	for _, stage := range []string{"construction", "consume", "unconsumed"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var event inference.Observation
				count := 0
				account := inference.New(inference.WithSettlementSink(func(_ context.Context, _ inference.Settlement) {
					close(entered)
					<-release
				}))
				source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
					time.Sleep(time.Second)
					if stage == "construction" {
						panic("construction panic")
					}
					return func(func(llm.CompletionChunk, error) bool) { t.Error("unconsumed stream invoked") }
				})
				go func() {
					defer close(done)
					defer func() {
						got := recover()
						if (got == nil) != (stage == "unconsumed") {
							t.Errorf("stage %s panic = %v", stage, got)
						}
					}()
					if err := (inference.Scope{Account: account}).InvokeObserved(t.Context(), inference.Call{}, source, llm.CompletionRequest{}, func(llm.CompletionStream) {
						if stage == "consume" {
							panic("consume panic")
						}
					}, func(_ context.Context, o inference.Observation) { event = o; count++ }); err != nil {
						t.Error(err)
					}
				}()
				<-entered
				time.Sleep(10 * time.Second)
				if count != 0 {
					t.Errorf("observer ran during settlement callback")
				}
				close(release)
				<-done
				if count != 1 || event.Duration != time.Second || event.Outcome != inference.StreamOutcomes.INTERRUPTED {
					t.Fatalf("fallback observation = %+v, count = %d", event, count)
				}
			})
		})
	}
}

func TestInvokeObservedOutcomeErrorIdentity(t *testing.T) {
	errQuota := errors.New("independent provider quota error")
	customCause := errors.New("unrelated caller cancellation cause")
	for _, enabled := range []bool{false, true} {
		name := "zero-scope"
		if enabled {
			name = "account-scope"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name                          string
				terminal, cancelCause         error
				cleanupCause, downstreamCause error
				want                          inference.StreamOutcome
			}{
				{name: "independent-error-after-custom-cancel", terminal: errQuota, cancelCause: customCause, want: inference.StreamOutcomes.FAILED},
				{name: "independent-error-without-cancel", terminal: errQuota, want: inference.StreamOutcomes.FAILED},
				{name: "wrapped-matching-custom-cause", terminal: fmt.Errorf("provider stopped: %w", customCause), cancelCause: customCause, want: inference.StreamOutcomes.CANCELLED},
				{name: "wrapped-context-canceled", terminal: fmt.Errorf("provider: %w", context.Canceled), want: inference.StreamOutcomes.CANCELLED},
				{name: "wrapped-context-deadline", terminal: fmt.Errorf("provider: %w", context.DeadlineExceeded), want: inference.StreamOutcomes.CANCELLED},
				{name: "EOF-then-cleanup-cancel", cleanupCause: customCause, want: inference.StreamOutcomes.COMPLETED},
				{name: "error-then-matching-cleanup-cancel", terminal: errQuota, cleanupCause: errQuota, want: inference.StreamOutcomes.FAILED},
				{name: "error-then-matching-downstream-cancel", terminal: errQuota, downstreamCause: errQuota, want: inference.StreamOutcomes.FAILED},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx, cancel := context.WithCancelCause(t.Context())
					defer cancel(nil)
					var settlement inference.Settlement
					settlements := 0
					account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) {
						settlement = s
						settlements++
					}))
					scope := inference.Scope{}
					if enabled {
						scope.Account = account
					}
					source := observedClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
						return func(yield func(llm.CompletionChunk, error) bool) {
							// Cancellation must happen inside the stream, after admission.
							if enabled {
								if got := account.Snapshot(); got.Admitted != 1 || got.Active != 1 || got.Settled != 0 {
									t.Fatalf("before stream: %+v", got)
								}
							}
							if tc.cleanupCause != nil {
								defer cancel(tc.cleanupCause)
							}
							if tc.cancelCause != nil {
								cancel(tc.cancelCause)
							}
							if tc.terminal != nil {
								yield(llm.CompletionChunk{}, tc.terminal)
							}
						}
					})
					var consumedErr error
					observations := 0
					err := scope.InvokeObserved(ctx, inference.Call{}, source, llm.CompletionRequest{}, func(stream llm.CompletionStream) {
						stream(func(_ llm.CompletionChunk, err error) bool {
							consumedErr = err
							if tc.downstreamCause != nil {
								cancel(tc.downstreamCause)
							}
							return err == nil
						})
					}, func(_ context.Context, o inference.Observation) {
						observations++
						if o.Outcome != tc.want {
							t.Errorf("outcome = %s, want %s", o.Outcome, tc.want)
						}
						if enabled {
							if got := account.Snapshot(); settlements != 1 || got.Admitted != 1 || got.Settled != 1 || got.Active != 0 {
								t.Errorf("at observation: settlements = %d, account = %+v", settlements, got)
							}
						}
					})
					if err != nil || observations != 1 {
						t.Fatalf("admission = %v, observations = %d", err, observations)
					}
					if !errors.Is(consumedErr, tc.terminal) {
						t.Errorf("downstream error = %v, want identity %v", consumedErr, tc.terminal)
					}
					if enabled {
						if got := account.Snapshot(); settlements != 1 || got.Admitted != 1 || got.Settled != 1 || got.Active != 0 {
							t.Errorf("after observation: settlements = %d, account = %+v", settlements, got)
						}
						wantErr := tc.terminal
						if wantErr == nil {
							wantErr = tc.cleanupCause // Preserve accounting's existing cleanup-cause semantics.
						}
						if !errors.Is(settlement.Err, wantErr) {
							t.Errorf("settlement error = %v, want identity %v", settlement.Err, wantErr)
						}
					} else if settlements != 0 || account.Snapshot() != (inference.Snapshot{}) {
						t.Errorf("zero scope used account: settlements = %d, account = %+v", settlements, account.Snapshot())
					}
				})
			}
		})
	}
}
