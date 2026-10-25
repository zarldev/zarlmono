package inference_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAccountLazyFinalSnapshotAndUnknown(t *testing.T) {
	var events []inference.Settlement
	var account *inference.Account
	account = inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) {
		_ = account.Snapshot() // Observers may inspect without lock reentry deadlock.
		events = append(events, s)
	}))
	scope := inference.Scope{Account: account, Member: "reviewer"}
	ctx := taskscope.WithID(t.Context(), "child")
	scope.Wrap(ctx, inference.Call{}, func(func(llm.CompletionChunk, error) bool) { t.Fatal("uninvoked stream ran") })
	if account.Snapshot().Admitted != 0 {
		t.Fatal("construction charged inference")
	}
	scope.Wrap(ctx, inference.Call{Operation: "runner"}, func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}}, nil) {
			return
		}
		yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 7, CompletionTokens: 4, ReasoningTokens: 2, ReasoningTokensReported: true}}, nil)
	})(func(llm.CompletionChunk, error) bool { return true })
	scope.Wrap(ctx, inference.Call{Operation: "planner"}, func(func(llm.CompletionChunk, error) bool) {})(func(llm.CompletionChunk, error) bool { return true })
	scope.Wrap(ctx, inference.Call{Operation: "judge"}, func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{UsageReported: true}, nil)
	})(func(llm.CompletionChunk, error) bool { return true })
	got := account.Snapshot()
	if got.Admitted != 3 || got.Settled != 3 || got.Active != 0 || got.Unknown != 1 || got.Tokens != 11 || got.Usage.PromptTokens != 7 || got.Usage.ReasoningTokens != 2 {
		t.Fatalf("snapshot=%+v", got)
	}
	if len(events) != 3 || events[0].ID == events[1].ID || events[0].TaskID != "child" || events[0].Member != "reviewer" {
		t.Fatalf("events=%+v", events)
	}
}

func TestConcurrentAdmissionIsAtomic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		account := inference.New(inference.WithLimits(inference.Limits{Invocations: 2}))
		scope := inference.Scope{Account: account}
		var invoked, denied atomic.Int32
		release := make(chan struct{})
		ctx := t.Context()
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				scope.Wrap(ctx, inference.Call{}, func(func(llm.CompletionChunk, error) bool) { invoked.Add(1); <-release })(func(_ llm.CompletionChunk, err error) bool {
					if errors.Is(err, inference.ErrBudget) {
						denied.Add(1)
					}
					return true
				})
			})
		}
		synctest.Wait()
		if invoked.Load() != 2 || denied.Load() != 10 {
			t.Errorf("invoked=%d denied=%d", invoked.Load(), denied.Load())
		}
		close(release)
		wg.Wait()
		got := account.Snapshot()
		if got.Admitted != 2 || got.Active != 0 || got.Denied != 10 || !got.Exhausted {
			t.Fatalf("snapshot=%+v", got)
		}
	})
}

func TestCountLimitSignalsAfterAllAdmittedSettle(t *testing.T) {
	account := inference.New(inference.WithLimits(inference.Limits{Invocations: 2}))
	scope := inference.Scope{Account: account}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			scope.Wrap(t.Context(), inference.Call{}, func(func(llm.CompletionChunk, error) bool) {
				started <- struct{}{}
				<-release
			})(func(llm.CompletionChunk, error) bool { return true })
		})
	}
	<-started
	<-started

	var denied error
	scope.Wrap(t.Context(), inference.Call{}, func(func(llm.CompletionChunk, error) bool) {
		t.Error("denied stream invoked")
	})(func(_ llm.CompletionChunk, err error) bool {
		denied = err
		return true
	})
	if !errors.Is(denied, inference.ErrBudget) {
		t.Fatalf("denied error = %v", denied)
	}
	select {
	case <-account.Exhausted():
		t.Fatal("count exhaustion signaled with admitted calls active")
	default:
	}

	close(release)
	wg.Wait()
	select {
	case <-account.Exhausted():
	default:
		t.Fatal("count exhaustion was not signaled after admitted calls settled")
	}
}

func TestFailureStopAndPanicSettle(t *testing.T) {
	for _, kind := range []string{"error", "stop", "panic"} {
		t.Run(kind, func(t *testing.T) {
			var event inference.Settlement
			account := inference.New(inference.WithLimits(inference.Limits{Tokens: 5}), inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { event = s }))
			scope := inference.Scope{Account: account}
			failure := errors.New("provider stream")
			execute := func() {
				scope.Wrap(t.Context(), inference.Call{}, func(yield func(llm.CompletionChunk, error) bool) {
					if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{TotalTokens: 7}}, nil) {
						return
					}
					if kind == "panic" {
						panic("provider panic")
					}
					yield(llm.CompletionChunk{}, failure)
				})(func(llm.CompletionChunk, error) bool { return kind != "stop" })
			}
			if kind == "panic" {
				func() {
					defer func() {
						if recover() == nil {
							t.Error("panic swallowed")
						}
					}()
					execute()
				}()
			} else {
				execute()
			}
			got := account.Snapshot()
			if got.Active != 0 || got.Tokens != 7 || !got.Exhausted {
				t.Fatalf("snapshot=%+v", got)
			}
			if kind == "panic" && !event.Interrupted || kind == "stop" && !event.Stopped || kind == "error" && !errors.Is(event.Err, failure) {
				t.Fatalf("event=%+v", event)
			}
		})
	}
}
