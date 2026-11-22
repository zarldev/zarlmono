package engine_test

import (
	"bytes"
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestHeadlessChildRecursiveRetriesAndFrozenDownstream(t *testing.T) {
	fixture := recordedAccountingFixture()
	var mu sync.Mutex
	calls := make(map[taskscope.ID]int)
	entered := 0
	siblings := make(chan struct{})
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		id, depth := taskscope.IDFrom(ctx), taskscope.DepthFrom(ctx)
		mu.Lock()
		calls[id]++
		attempt := calls[id]
		if depth == 1 && attempt == 1 {
			entered++
			if entered == 2 {
				close(siblings)
			}
		}
		mu.Unlock()
		return func(yield func(llm.CompletionChunk, error) bool) {
			if depth == 1 && attempt == 1 {
				select {
				case <-siblings:
				case <-ctx.Done():
					return
				}
			}
			if !yield(llm.CompletionChunk{UsageReported: true, Usage: *fixture.Usage, PricingUsage: fixture.PricingUsage,
				Execution: &llm.ExecutionObservation{GatewayCallID: "private-id-canary", ReportedCost: llm.ReportedCost{Reported: true, Amount: 7, Currency: "JPY", Source: "private-source-canary"}}}, nil) {
				return
			}
			if attempt == 1 && depth < 2 {
				yield(llm.CompletionChunk{}, &llm.RateLimitError{Retryable: true, RetryAfter: time.Nanosecond})
				return
			}
			switch {
			case depth == 0 && attempt == 2:
				yield(childSpawnChunk(`{"prompt":"identical private child assignment","mode":"explore"}`, `{"prompt":"identical private child assignment","mode":"explore"}`), nil)
			case depth == 1 && attempt == 2:
				yield(childSpawnChunk(`{"prompt":"private grandchild assignment","mode":"explore"}`), nil)
			default:
				yield(llm.CompletionChunk{Content: "Read-only evidence complete.", FinishReason: llm.FinishReasons.STOP}, nil)
			}
		}
	})
	live, store, _ := headlessChildLive(t, provider, engine.WithLiveSink(&childCaptureSink{}))
	// This sink mutates the event after the recorder has captured it.
	live.SetLimits(1024, 12, 8, 3)
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "actual-route", Model: "frozen-model"}, RateCard: fixture.RateCard})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	result := live.RunHeadless(ctx, "private root assignment", 12)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	rows := headlessChildRows(t, store, string(result.ID))
	if len(rows) < 8 {
		t.Fatalf("expected sibling/recursive retries, got %d", len(rows))
	}
	byTask := make(map[string]int)
	for _, row := range rows {
		got, err := engine.DecodeRecordedHeadlessChildProviderAccounting(row.Accounting.PayloadJSON)
		if err != nil {
			t.Fatal(err)
		}
		if got.RunID != string(result.ID) || got.ChildTaskID != row.Accounting.ChildTaskID || got.Attempt != byTask[got.ChildTaskID]+1 || got.Provider != "actual-route" || got.Model != "frozen-model" || !reflect.DeepEqual(got.Usage, fixture.Usage) || !reflect.DeepEqual(got.RateCard, fixture.RateCard) || !reflect.DeepEqual(got.ConfiguredEstimate, fixture.ConfiguredEstimate) || got.ReportedCost.Source != "" {
			t.Fatalf("child occurrence: %+v", got)
		}
		byTask[got.ChildTaskID]++
		for _, secret := range []string{"private-id-canary", "private-source-canary", "private child assignment", "private grandchild assignment"} {
			if bytes.Contains(row.Accounting.PayloadJSON, []byte(secret)) {
				t.Fatalf("private evidence leaked: %s", secret)
			}
		}
	}
	if len(byTask) < 4 {
		t.Fatalf("sibling/grandchild identities merged: %+v", byTask)
	}
}
