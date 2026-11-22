package engine_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

type headlessChildRetrySink struct {
	runner.NopSink
	mu      sync.Mutex
	settled []taskscope.ID
}

func (*headlessChildRetrySink) DiffEvent(diffrecorder.DiffEvent) {}
func (*headlessChildRetrySink) PlanUpdated(string, code.Plan)    {}

func (s *headlessChildRetrySink) OnProviderAttemptSettled(_ context.Context, event runner.ProviderAttemptSettled) {
	if event.Depth == 0 {
		return
	}
	s.mu.Lock()
	s.settled = append(s.settled, event.TaskID)
	s.mu.Unlock()
}

func (s *headlessChildRetrySink) childIDs() []taskscope.ID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]taskscope.ID(nil), s.settled...)
}

func TestHeadlessChildRetryFrozenBatchAfterAttemptFailure(t *testing.T) {
	fixture := recordedAccountingFixture()
	card := *fixture.RateCard
	var live *engine.LiveRunner
	var store *db.Store
	var roots int
	checked := false
	sink := &headlessChildRetrySink{}
	var provider recordedAccountingProvider
	provider = func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		if taskscope.DepthFrom(ctx) > 0 {
			return func(yield func(llm.CompletionChunk, error) bool) {
				usage := *fixture.Usage
				usage.PromptTokens = 31
				yield(llm.CompletionChunk{Content: "Read-only evidence complete.", FinishReason: llm.FinishReasons.STOP,
					UsageReported: true, Usage: usage, PricingUsage: fixture.PricingUsage,
					Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true, Amount: 7, Currency: "USD", Source: "private-source"}}}, nil)
			}
		}
		roots++
		call := roots
		if call == 3 {
			// The next real root admission follows the failed first-attempt checkpoint.
			checked = true
			id := string(taskscope.IDFrom(ctx))
			if got := headlessRows(t, store, id); len(got) != 0 {
				t.Fatalf("partial root rows: %+v", got)
			}
			if got := headlessChildRows(t, store, id); len(got) != 0 {
				t.Fatalf("partial child rows: %+v", got)
			}
			if got, err := store.ListHeadlessAttempts(ctx, id); err != nil || len(got) != 0 {
				t.Fatalf("partial attempts: %+v %v", got, err)
			}
			if got, err := store.GetHeadlessRun(ctx, id); err != nil || got.EndedAt != nil {
				t.Fatalf("partial completion: %+v %v", got, err)
			}
			if ids := sink.childIDs(); len(ids) != 1 {
				t.Fatalf("first child not settled at retry: %+v", ids)
			}
			headlessSQL(t, store, `DROP TRIGGER reject_child_retry_attempt`)
			card.Currency = "later-currency"
			live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "later-route", Model: "later-model"}, RateCard: &card})
		}
		return func(yield func(llm.CompletionChunk, error) bool) {
			if call == 1 || call == 3 {
				yield(childSpawnChunk(`{"prompt":"retry child","mode":"explore"}`), nil)
				return
			}
			yield(llm.CompletionChunk{Content: "root completed", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	}
	live, store, _ = headlessChildLive(t, provider, engine.WithLiveSink(sink))
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "original-route", Model: "original-model"}, RateCard: fixture.RateCard})
	live.SetLimits(1024, 2, 4, 2)
	live.SetVerifyLoop("false", 2)
	headlessSQL(t, store, `CREATE TRIGGER reject_child_retry_attempt BEFORE INSERT ON headless_attempts BEGIN SELECT RAISE(ABORT,'retry canary'); END`)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := live.RunHeadless(ctx, "retry children", 2)
	if !checked || result.Err != nil || result.Reason != runner.TerminalMaxIterations || result.FinalContent != "root completed" {
		t.Fatalf("retry verdict/fixture: checked=%v result=%+v", checked, result)
	}
	id := string(result.ID)
	rows := headlessChildRows(t, store, id)
	ids := sink.childIDs()
	if len(rows) != 2 || len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("child identity/count: rows=%+v ids=%+v", rows, ids)
	}
	for i, row := range rows {
		got, err := engine.DecodeRecordedHeadlessChildProviderAccounting(row.Accounting.PayloadJSON)
		if err != nil || got.ChildTaskID != string(ids[i]) || got.Attempt != 1 || got.Usage == nil || got.Usage.PromptTokens != 31 || got.ReportedCost == nil || got.ReportedCost.Amount != 7 || got.ReportedCost.Source != "" {
			t.Fatalf("child %d changed identity/evidence: %+v %v", i, got, err)
		}
		if got.Provider != "original-route" || got.Model != "original-model" || got.RateCard == nil || got.ConfiguredEstimate == nil || got.PricingUsage == nil {
			t.Fatalf("child %d lost capture: %+v", i, got)
		}
		if i == 0 && (!reflect.DeepEqual(got.ConfiguredEstimate, fixture.ConfiguredEstimate) || !reflect.DeepEqual(got.RateCard, fixture.RateCard)) {
			t.Fatalf("frozen card changed: %+v", got.RateCard)
		}
	}
	attempts, err := store.ListHeadlessAttempts(ctx, id)
	rootRows := headlessRows(t, store, id)
	if err != nil || len(attempts) != 2 || len(rootRows) != 4 {
		t.Fatalf("retry root/attempt rows: roots=%d calls=%d attempts=%+v err=%v", len(rootRows), roots, attempts, err)
	}
	for _, attempt := range attempts {
		if attempt.TokensIn != nil || attempt.TokensOut != nil {
			t.Fatalf("child usage charged to root: %+v", attempt)
		}
	}
	for i, row := range rootRows {
		got := decodeRecordedHeadlessAccounting(t, row.Accounting.PayloadJSON)
		wantAttempt, wantOrdinal := i/2+1, i%2+1
		if got.PursueAttempt != wantAttempt || got.ProviderOrdinal != wantOrdinal {
			t.Fatalf("root re-drive identity: %+v", got)
		}
	}
	if run, err := store.GetHeadlessRun(ctx, id); err != nil || run.EndedAt == nil {
		t.Fatalf("retry completion: %+v %v", run, err)
	}
	var path string
	if err := store.DB().QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if later := headlessChildRows(t, reopened, id); !reflect.DeepEqual(later, rows) {
		t.Fatalf("reopen changed child rows: %+v", later)
	}
}
