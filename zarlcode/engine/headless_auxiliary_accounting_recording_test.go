package engine_test

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

type headlessAuxiliaryTerminalSink struct {
	runner.NopSink
	terminal, release chan struct{}
}

func (*headlessAuxiliaryTerminalSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*headlessAuxiliaryTerminalSink) PlanUpdated(string, code.Plan)    {}
func (s *headlessAuxiliaryTerminalSink) OnConversationEnded(_ context.Context, event runner.ConversationEnded) {
	if event.Depth == 0 {
		close(s.terminal)
		<-s.release
	}
}

func TestHeadlessAuxiliaryAccountingLateChildDuringWriterWait(t *testing.T) {
	settings := headlessAuxiliarySettings(t)
	setRecordedAuxiliarySetting(t, settings, prefs.KeySpawnDefaultImplementModel, "actual-model")
	entered, release, captured := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	sink := &headlessAuxiliaryTerminalSink{terminal: make(chan struct{}), release: make(chan struct{})}
	unblockTerminal := sync.OnceFunc(func() { close(sink.release) })
	base := &auxiliaryRunProvider{}
	roots := 0
	provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
		if req.ResponseFormat.Name == "decompose_verdict" && taskscope.DepthFrom(ctx) > 0 {
			return func(yield func(llm.CompletionChunk, error) bool) {
				close(entered)
				<-release // Deliberately retain the real helper through root termination.
				e := auxiliaryEvidence()
				yield(llm.CompletionChunk{Content: `{"action":"smaller_scope","rationale":"late child"}`, UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil)
				e.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 99999
			}
		}
		if req.ResponseFormat.Name != "" || taskscope.DepthFrom(ctx) > 0 {
			return base.Complete(ctx, req)
		}
		roots++
		n := roots
		return func(yield func(llm.CompletionChunk, error) bool) {
			if n == 1 {
				yield(childSpawnChunk(`{"prompt":"late child","mode":"implement"}`), nil)
				return
			}
			select {
			case <-entered:
			case <-ctx.Done():
				yield(llm.CompletionChunk{}, ctx.Err())
				return
			}
			yield(llm.CompletionChunk{Content: "root complete", UsageReported: true, Usage: llm.Usage{PromptTokens: 2, TotalTokens: 2}}, nil)
		}
	})
	live := recordedAuxiliaryLive(t, settings, provider, engine.WithLiveSink(sink), engine.WithAuxiliaryObservationSink(func(_ context.Context, o engine.AuxiliaryObservation) {
		if o.Kind == engine.AuxiliaryKinds.JUDGE && o.Inference.Depth > 0 {
			mutateAuxiliary(&o)
			close(captured)
		}
	}))
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Model: "actual-model"}, RateCard: recordedAccountingFixture().RateCard})
	live.SetLimits(512, 6, 6, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan engine.HeadlessResult, 1)
	go func() { done <- live.RunHeadlessWithObservations(ctx, "root terminates while child helper lives", 6) }()
	joined := false
	var closeConn func()
	defer func() {
		cancel()
		unblock()
		unblockTerminal()
		if closeConn != nil {
			closeConn()
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-sink.terminal:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	runs, err := settings.Store.ListHeadlessRunsByWorkspace(ctx, settings.WorkspaceRoot(), 10)
	if err != nil || len(runs) != 1 || runs[0].EndedAt != nil {
		t.Fatalf("premature completion: %+v %v", runs, err)
	}
	conn, err := settings.Store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	closeConn = sync.OnceFunc(func() { _ = conn.Close() })
	before := settings.Store.DB().Stats().WaitCount
	unblockTerminal()
	waitHeadlessWriter(t, settings.Store, before)
	// The checkpoint now owns an older planner snapshot and waits BEFORE BeginTx
	// acquisition, not inside COMMIT. The child helper settles into a newer append.
	unblock()
	select {
	case <-captured:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-done:
		joined = true
		t.Fatal("returned through held writer")
	default:
	}
	closeConn()
	result := <-done
	joined = true
	if result.Task.Err != nil {
		t.Fatal(result.Task.Err)
	}
	rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
	if len(rows) != 2 {
		t.Fatalf("rows=%d; older ACK discarded late child or duplicated evidence", len(rows))
	}
	purposes := make(map[engine.AuxiliaryAccountingPurpose]int)
	for _, row := range rows {
		got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(row.PayloadJSON)
		if err != nil || got.RunID != runs[0].ID || got.Usage == nil || got.Usage.PromptTokens != 1000 || got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 4 {
			t.Fatalf("late frozen evidence %+v %v", got, err)
		}
		purposes[got.Purpose]++
	}
	if purposes[engine.AuxiliaryAccountingPurposes.PURPOSEJUDGE] != 1 || purposes[engine.AuxiliaryAccountingPurposes.PURPOSESPAWNPLANNER] != 1 {
		t.Fatal(purposes)
	}
	run, err := settings.Store.GetHeadlessRun(ctx, runs[0].ID)
	if err != nil || run.EndedAt == nil || run.FinalContent != "root complete" {
		t.Fatalf("completion %+v %v", run, err)
	}
	attempts, err := settings.Store.ListHeadlessAttempts(ctx, runs[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].FinalContent != "root complete" {
		t.Fatalf("summary %+v %v", attempts, err)
	}
	var path string
	if err := settings.Store.DB().QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := headlessAuxiliaryRows(t, reopened, runs[0].ID); !cmp.Equal(got, rows) {
		t.Fatal("reopen changed exact UUID/version/bytes")
	}
}

func TestHeadlessAuxiliaryAccountingAttemptFailureRetriesWholeFrozenBatch(t *testing.T) {
	settings := headlessAuxiliarySettings(t)
	headlessSQL(t, settings.Store, `CREATE TRIGGER reject_aux_attempt BEFORE INSERT ON headless_attempts BEGIN SELECT RAISE(ABORT,'private attempt canary'); END`)
	base := &auxiliaryRunProvider{}
	checked := false
	card := recordedAccountingFixture().RateCard
	var live *engine.LiveRunner
	provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
		if req.ResponseFormat.Name == "" && base.mainCalls == 4 && !checked {
			// This is the next real provider admission, AFTER the failed checkpoint.
			checked = true
			id := string(taskscope.IDFrom(ctx))
			if got := headlessAuxiliaryRows(t, settings.Store, id); len(got) != 0 {
				t.Fatal("failed checkpoint leaked auxiliaries")
			}
			if got := headlessRows(t, settings.Store, id); len(got) != 0 {
				t.Fatal("failed checkpoint leaked MAIN")
			}
			attempts, err := settings.Store.ListHeadlessAttempts(ctx, id)
			if err != nil || len(attempts) != 0 {
				t.Fatalf("atomic summaries=%+v %v", attempts, err)
			}
			run, err := settings.Store.GetHeadlessRun(ctx, id)
			if err != nil || run.EndedAt != nil {
				t.Fatalf("premature completion %+v %v", run, err)
			}
			// Later evidence/settings cannot reprice already-encoded pending bytes.
			card.Currency = "changed-after-failure"
			setRecordedAuxiliarySetting(t, settings, prefs.KeyJudgeModel, "later-model")
			live.ApplyTarget(engine.TargetUpdate{Provider: base, Spec: engine.ProviderSpec{Name: "later-route", Model: "later-model"}})
			headlessSQL(t, settings.Store, `DROP TRIGGER reject_aux_attempt`)
		}
		return base.Complete(ctx, req)
	})
	live = recordedAuxiliaryLive(t, settings, provider)
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "original-route", Model: "original-model"}, RateCard: card})
	live.SetVerifyLoop("false", 2)
	result := live.RunHeadlessWithObservations(t.Context(), "retry immutable evidence", 6)
	if !checked {
		t.Fatal("fixture did not enter second attempt after failure")
	}
	rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
	if len(rows) != 1 {
		t.Fatalf("aux rows=%d", len(rows))
	}
	got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(rows[0].PayloadJSON)
	expectedEstimate := backends.EstimatePrice(*recordedAccountingFixture().RateCard, *auxiliaryEvidence().PricingUsage)
	if !cmp.Equal(got.ConfiguredEstimate, &expectedEstimate, cmpopts.EquateComparable(llm.BillingMode{}, llm.MetadataSource{}, llm.PricingIssueKind{})) {
		t.Fatal("retry repriced frozen configured estimate")
	}
	if err != nil || got.Provider != "original-route" || got.Model != "original-model" || !cmp.Equal(got.RateCard, recordedAccountingFixture().RateCard, cmpopts.EquateComparable(llm.BillingMode{}, llm.MetadataSource{})) || got.Usage.PromptTokens != 1000 {
		t.Fatalf("reconstructed/repriced capture %+v %v", got, err)
	}
	attempts, err := settings.Store.ListHeadlessAttempts(t.Context(), string(result.Task.ID))
	if err != nil || len(attempts) != 2 {
		t.Fatalf("retained summaries %+v %v", attempts, err)
	}
	if n := len(headlessRows(t, settings.Store, string(result.Task.ID))); n != base.mainCalls {
		t.Fatalf("MAIN loss/duplication rows=%d calls=%d", n, base.mainCalls)
	}
	run, err := settings.Store.GetHeadlessRun(t.Context(), string(result.Task.ID))
	if err != nil || run.EndedAt == nil {
		t.Fatalf("final checkpoint %+v %v", run, err)
	}
	var path string
	if err := settings.Store.DB().QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if later := headlessAuxiliaryRows(t, reopened, string(result.Task.ID)); !cmp.Equal(later, rows) {
		t.Fatal("reopen changed frozen evidence")
	}
}

func TestHeadlessAuxiliaryAccountingFinalFailureDoesNotClaimDurability(t *testing.T) {
	settings := headlessAuxiliarySettings(t)
	headlessSQL(t, settings.Store, `CREATE TRIGGER reject_aux_completion BEFORE UPDATE OF ended_at ON headless_runs BEGIN SELECT RAISE(ABORT,'private completion canary'); END`)
	p := &auxiliaryRunProvider{}
	live := recordedAuxiliaryLive(t, settings, p)
	result := live.RunHeadlessWithObservations(t.Context(), "best effort final checkpoint", 6)
	if result.Task.Err != nil || result.Task.Reason != runner.TerminalCompleted {
		t.Fatalf("persistence changed task verdict %+v", result.Task)
	}
	run, err := settings.Store.GetHeadlessRun(t.Context(), string(result.Task.ID))
	if err != nil || run.EndedAt != nil {
		t.Fatalf("false durable completion %+v %v", run, err)
	}
	if rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID)); len(rows) != 1 {
		t.Fatalf("earlier checkpoint evidence lost: %d", len(rows))
	}
}

func TestHeadlessAuxiliaryAccountingCapturedBeforeBlockingSemanticCallback(t *testing.T) {
	settings := headlessAuxiliarySettings(t)
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	p := &auxiliaryRunProvider{}
	live := recordedAuxiliaryLive(t, settings, p, engine.WithAuxiliaryObservationSink(func(_ context.Context, o engine.AuxiliaryObservation) {
		mutateAuxiliary(&o)
		close(entered)
		<-release
	}))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan engine.HeadlessResult, 1)
	go func() { done <- live.RunHeadlessWithObservations(ctx, "blocked semantic callback", 6) }()
	joined := false
	defer func() {
		cancel()
		unblock()
		if !joined {
			<-done
		}
	}()
	select {
	case <-entered:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	cancel() // Synthetic attempt checkpoint may now save the already-captured row.
	deadline, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	var runID string
	var frozen []db.HeadlessAttemptRecord
	for len(frozen) == 0 {
		runs, err := settings.Store.ListHeadlessRunsByWorkspace(deadline, settings.WorkspaceRoot(), 10)
		if err != nil || len(runs) != 1 || runs[0].EndedAt != nil {
			t.Fatalf("premature completion %+v %v", runs, err)
		}
		runID = runs[0].ID
		frozen, err = settings.Store.ListHeadlessAttempts(deadline, runID)
		if err != nil {
			t.Fatal(err)
		}
		if deadline.Err() != nil {
			t.Fatal(deadline.Err())
		}
		runtime.Gosched()
	}
	rows := headlessAuxiliaryRows(t, settings.Store, runID)
	if len(rows) != 1 {
		t.Fatalf("callback preceded capture: rows=%d", len(rows))
	}
	got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(rows[0].PayloadJSON)
	if err != nil || got.Usage.PromptTokens != 1000 || got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 4 {
		t.Fatalf("semantic mutation changed durable bytes %+v %v", got, err)
	}
	select {
	case <-done:
		joined = true
		t.Fatal("returned before callback drained")
	default:
	}
	unblock()
	<-done
	joined = true
	if later := headlessAuxiliaryRows(t, settings.Store, runID); !cmp.Equal(later, rows) {
		t.Fatal("final flush changed captured bytes")
	}
	attempts, err := settings.Store.ListHeadlessAttempts(t.Context(), runID)
	if err != nil || !cmp.Equal(attempts, frozen) {
		t.Fatalf("eventual result rewrote synthetic summary %+v %v", attempts, err)
	}
}
