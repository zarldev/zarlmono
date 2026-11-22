package tui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

type auxiliaryPersistProvider struct {
	mu      sync.Mutex
	calls   map[taskscope.ID]int
	started chan struct{}
	late    bool
}

func (*auxiliaryPersistProvider) Name() string { return "openai" }
func (p *auxiliaryPersistProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if req.ResponseFormat.Name == "decompose_verdict" {
			yield(llm.CompletionChunk{Content: `{"action":"smaller_scope","rationale":"synthetic verdict"}`, UsageReported: true, Usage: llm.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true, Tokens: 7}}}, Execution: &llm.ExecutionObservation{GatewayCallID: "synthetic-call-canary", ReportedCost: llm.ReportedCost{Reported: true, Amount: 42, Currency: "JPY", Source: "synthetic-source-canary"}}}, nil)
			if p.late {
				close(p.started)
				<-ctx.Done()
				yield(llm.CompletionChunk{}, context.Cause(ctx))
			}
			return
		}
		p.mu.Lock()
		if p.calls == nil {
			p.calls = make(map[taskscope.ID]int)
		}
		id := taskscope.IDFrom(ctx)
		p.calls[id]++
		n := p.calls[id]
		if p.late && taskscope.DepthFrom(ctx) == 0 && n == 1 {
			p.started = make(chan struct{})
		}
		started := p.started
		p.mu.Unlock()
		if p.late && taskscope.DepthFrom(ctx) == 0 {
			if n == 1 {
				yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{ID: "spawn-" + string(id), Type: "function", Function: llm.ToolCallFunction{Name: "agent_spawn", Arguments: `{"agent":"aux-persist-child","prompt":"synthetic child prompt canary","mode":"explore"}`}}}}, nil)
				return
			}
			select {
			case <-started:
			case <-ctx.Done():
				return
			}
			yield(llm.CompletionChunk{Content: "Root ends with a live child judge."}, nil)
			return
		}
		if n <= 3 {
			yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}, ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("read-%s-%d", id, n), Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"synthetic-missing-canary"}`}}}}, nil)
			return
		}
		yield(llm.CompletionChunk{Content: "Inspection complete."}, nil)
	}
}

type auxiliaryPersistSink struct {
	*teasink.Sink
	ended atomic.Bool
}

func (s *auxiliaryPersistSink) OnConversationEnded(ctx context.Context, e runner.ConversationEnded) {
	if e.Depth == 0 {
		s.ended.Store(true)
	}
	s.Sink.OnConversationEnded(ctx, e)
}

func newAuxiliaryPersistFixture(t *testing.T, late bool) (*beforeFixture, *atomic.Bool) {
	t.Helper()
	f := newBeforeFixture(t)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	for key, value := range map[string]string{prefs.KeyJudgeProvider: prefs.ActiveModelTarget, prefs.KeyCompactProvider: prefs.ActiveModelTarget, prefs.KeyCompactionMode: "manual", prefs.KeyProgrammaticTools: "off", prefs.KeySpawnDefaultExploreProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultVerifyProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget} {
		if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(f.ws.Root(), ".zarlcode", "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aux-persist-child.md"), []byte("---\nname: aux-persist-child\ndescription: Persistence fixture\nmode: explore\n---\nInspect read-only evidence.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &auxiliaryPersistProvider{late: late}
	sink := &auxiliaryPersistSink{Sink: f.sink}
	childLate := &atomic.Bool{}
	live := engine.NewLiveRunner(p, f.ws, "saved-model", engine.WithSettings(settings), engine.WithLiveSink(sink), engine.WithAuxiliaryObservationSink(func(_ context.Context, o engine.AuxiliaryObservation) {
		if o.Inference.Depth > 0 {
			childLate.Store(sink.ended.Load())
		}
	}))
	live.SetProviderSpec(p, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	limit := 6
	if late {
		limit = 2
	}
	live.SetLimits(64, limit, 6, 2)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	f.live = live
	f.ui.SetLiveRunner(live)
	f.ui.SetStartupReady(true)
	return f, childLate
}

func pendingAuxiliaries(t *testing.T, f *beforeFixture) []db.SessionAuxiliaryProviderAccounting {
	t.Helper()
	_, batch, err := f.live.RecordedHistoryWithAccountingBatch(f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	return batch.Auxiliaries
}
func durableAuxiliaries(t *testing.T, store *db.Store, id string) []db.SessionAuxiliaryProviderAccountingRecord {
	t.Helper()
	rows, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), id, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAuxiliaryAccountingActualRuntimeDrainAndReopen(t *testing.T) {
	f, late := newAuxiliaryPersistFixture(t, true)
	settleRewindTurn(t, f, "late descendant auxiliary stream")
	if !late.Load() {
		t.Fatal("child judge did not settle after root terminal")
	}
	id := f.ui.SessionIdentity()
	rows := durableAuxiliaries(t, f.store, id)
	if len(rows) != 1 || len(pendingAuxiliaries(t, f)) != 0 {
		t.Fatalf("durable=%d", len(rows))
	}
	record, err := engine.ReadRecordedSessionAuxiliaryAccounting(t.Context(), f.store, id, rows[0].Accounting.InvocationID)
	if err != nil || record.Purpose != engine.AuxiliaryAccountingPurposes.PURPOSEJUDGE || record.StreamOutcome != inference.StreamOutcomes.CANCELLED || record.Usage.TotalTokens != 10 || record.ReportedCost.Amount != 42 || record.ReportedCost.Source != "" {
		t.Fatalf("late evidence %+v %v", record, err)
	}
	_, batch, err := f.live.RecordedHistoryWithAccountingBatch(id)
	if err != nil || len(batch.Roots) != 0 || len(batch.Children) != 0 {
		t.Fatal("batch not acknowledged", err)
	}
	for _, canary := range []string{"synthetic child prompt canary", "synthetic-call-canary", "synthetic-source-canary", "synthetic-missing-canary"} {
		if bytes.Contains(rows[0].Accounting.PayloadJSON, []byte(canary)) {
			t.Fatal("privacy canary retained")
		}
	}
	var path string
	if err := f.store.DB().QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if !reflect.DeepEqual(rows, durableAuxiliaries(t, reopened, id)) {
		t.Fatal("reopen changed bytes")
	}
	usage := f.ui.UsageSnapshot()
	if usage.CostUSD != 0 || usage.CostParentUSD != 0 {
		t.Fatal("auxiliary booked as legacy USD")
	}
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, durableAuxiliaries(t, reopened, id)) {
		t.Fatal("flush duplicated evidence")
	}
}

func TestAuxiliaryAccountingActualFullSaveRollbackRetryAndBefore(t *testing.T) {
	for _, failure := range []string{"statement", "commit"} {
		t.Run(failure, func(t *testing.T) {
			f, _ := newAuxiliaryPersistFixture(t, true)
			trigger := `CREATE TRIGGER reject_aux BEFORE INSERT ON session_auxiliary_provider_accounting BEGIN SELECT RAISE(ABORT,'injected auxiliary failure'); END`
			if failure == "commit" {
				trigger = `CREATE TABLE deferred_aux_parent(id INTEGER PRIMARY KEY); CREATE TABLE deferred_aux_child(id INTEGER REFERENCES deferred_aux_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER reject_aux AFTER INSERT ON session_auxiliary_provider_accounting BEGIN INSERT INTO deferred_aux_child(id) VALUES (42); END`
			}
			if _, err := f.store.DB().ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			settleRewindTurn(t, f, "atomic failed full save")
			id := f.ui.SessionIdentity()
			first := pendingAuxiliaries(t, f)
			if len(first) != 1 || len(durableAuxiliaries(t, f.store, id)) != 0 {
				t.Fatal("failed commit released evidence")
			}
			roots, err := f.store.ListSessionProviderAccounting(t.Context(), id, 0, 256)
			if err != nil || len(roots) != 0 {
				t.Fatal("root set did not roll back", err)
			}
			children := durableChildren(t, f.store, id)
			if len(children) != 0 {
				t.Fatal("child set did not roll back")
			}
			before, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			openRecovery(f.ui)
			retry := recoveryKey(f.ui, 'r')
			if retry == nil {
				t.Fatal("missing retry")
			}
			failed := retry()
			f.ui.Update(failed)
			after, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(first, pendingAuxiliaries(t, f)) {
				t.Fatal("failed retry changed snapshot/state", err)
			}
			if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_aux`); err != nil {
				t.Fatal(err)
			}
			retry = recoveryKey(f.ui, 'r')
			if retry == nil {
				t.Fatal("missing retry")
			}
			f.ui.Update(failed)
			f.ui.Update(retry())
			rows := durableAuxiliaries(t, f.store, id)
			if len(rows) != 1 || !reflect.DeepEqual(rows[0].Accounting, first[0]) || len(pendingAuxiliaries(t, f)) != 0 {
				t.Fatal("retry changed bytes")
			}
			requireRecoveredContext(t, f, "")
			if err := f.live.RunRecordedTurn(t.Context(), "pending before next boundary", nil, f.store, id); err != nil {
				t.Fatal(err)
			}
			f.sink.Drain()
			f.mu.Lock()
			f.events = nil
			f.mu.Unlock()
			pending := pendingAuxiliaries(t, f)
			if len(pending) != 1 {
				t.Fatal("missing pending auxiliary")
			}
			if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_aux BEFORE INSERT ON session_auxiliary_provider_accounting BEGIN SELECT RAISE(ABORT,'injected BEFORE failure'); END`); err != nil {
				t.Fatal(err)
			}
			driveBeforeCommand(f, f.ui.Submit("BEFORE auxiliary must roll back"))
			f.applyEvents()
			if !reflect.DeepEqual(pending, pendingAuxiliaries(t, f)) || len(durableAuxiliaries(t, f.store, id)) != 1 {
				t.Fatal("failed BEFORE acknowledged evidence")
			}
			if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_aux`); err != nil {
				t.Fatal(err)
			}
			settleRewindTurn(t, f, "successful BEFORE and terminal")
			rows = durableAuxiliaries(t, f.store, id)
			if len(rows) != 3 || len(pendingAuxiliaries(t, f)) != 0 {
				t.Fatal("BEFORE lost evidence")
			}
			found := false
			for _, row := range rows {
				if reflect.DeepEqual(row.Accounting, pending[0]) {
					found = true
				}
			}
			if !found {
				t.Fatal("BEFORE repriced pending bytes")
			}
		})
	}
}

func TestAuxiliaryAccountingNonexactResumeAndRewindFork(t *testing.T) {
	f, _ := newAuxiliaryPersistFixture(t, false)
	settleRewindTurn(t, f, "first judge")
	settleRewindTurn(t, f, "second judge")
	id := f.ui.SessionIdentity()
	original := durableAuxiliaries(t, f.store, id)
	if len(original) != 2 {
		t.Fatalf("original=%d", len(original))
	}
	record, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	usage := bytes.Clone(record.LastUsageJSON)
	if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := f.ui.SaveSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	record, err = f.store.GetSession(t.Context(), id)
	if err != nil || !bytes.Equal(usage, record.LastUsageJSON) || !reflect.DeepEqual(original, durableAuxiliaries(t, f.store, id)) {
		t.Fatal("resume changed evidence", err)
	}
	previewRewindPrompt(f, 1)
	_, apply := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	fork := f.ui.SessionIdentity()
	if fork == id || len(durableAuxiliaries(t, f.store, fork)) != 0 {
		t.Fatal("fork copied source accounting")
	}
	driveBeforeCommand(f, apply)
	settleRewindTurn(t, f, "fork judge")
	forkRows := durableAuxiliaries(t, f.store, fork)
	if len(forkRows) != 1 {
		t.Fatal("missing fork own row")
	}
	for _, row := range original {
		if forkRows[0].Accounting.InvocationID == row.Accounting.InvocationID || forkRows[0].Accounting.OwnerRequestBatchID == row.Accounting.OwnerRequestBatchID {
			t.Fatal("fork reused source identity")
		}
	}
	if !reflect.DeepEqual(original, durableAuxiliaries(t, f.store, id)) {
		t.Fatal("fork changed source")
	}
}

func TestAuxiliaryAccountingNonexactFullSave(t *testing.T) {
	f, _ := newAuxiliaryPersistFixture(t, false)
	settleRewindTurn(t, f, "establish recorded judge session")
	fork := f.ui.SessionIdentity()
	// Legacy context encoding exercises ordinary full-save persistence without
	// claiming an exact checkpoint head; incurred rows retain their original bytes.
	record, err := f.store.GetSession(t.Context(), fork)
	if err != nil {
		t.Fatal(err)
	}
	record.ContextJSON, err = json.Marshal(f.live.ContextSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveActiveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	f.ui.SetLiveEventSink(nil)
	if err := f.ui.ResumeSavedSession(t.Context(), fork); err != nil {
		t.Fatal(err)
	}
	if err := f.live.RunRecordedTurn(t.Context(), "ordinary pending judge", nil, f.store, fork); err != nil {
		t.Fatal(err)
	}
	f.applyEvents()
	pending := pendingAuxiliaries(t, f)
	if len(pending) != 1 {
		t.Fatal("missing ordinary pending evidence")
	}
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := durableAuxiliaries(t, f.store, fork)
	if len(rows) != 2 || !reflect.DeepEqual(rows[1].Accounting, pending[0]) || len(pendingAuxiliaries(t, f)) != 0 {
		t.Fatal("ordinary full save lost evidence")
	}
}

func TestAuxiliaryAccountingLateConflictRollsBackActualFullSave(t *testing.T) {
	f, _ := newAuxiliaryPersistFixture(t, false)
	settleRewindTurn(t, f, "establish session")
	id := f.ui.SessionIdentity()
	if err := f.live.RunRecordedTurn(t.Context(), "pending helper for conflict", nil, f.store, id); err != nil {
		t.Fatal(err)
	}
	f.applyEvents()
	pending := pendingAuxiliaries(t, f)
	if len(pending) != 1 {
		t.Fatal("missing pending helper")
	}
	competing := pending[0].Clone()
	competing.PayloadJSON = append(competing.PayloadJSON, '\n')
	if err := f.store.SaveSessionAuxiliaryProviderAccounting(t.Context(), id, []db.SessionAuxiliaryProviderAccounting{competing}); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	roots := durableProviderAccounting(t, f, id)
	err = f.ui.SaveSession(t.Context())
	if !errors.Is(err, db.ErrAuxiliaryProviderAccountingConflict) {
		t.Fatalf("late conflict=%v", err)
	}
	after, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(roots, durableProviderAccounting(t, f, id)) || !reflect.DeepEqual(pending, pendingAuxiliaries(t, f)) {
		t.Fatal("late auxiliary conflict partially committed or acknowledged", err)
	}
}

func pendingAuxiliaryFullBatch(t *testing.T, f *beforeFixture) ([]db.SessionHistoryBatch, db.SessionAccountingBatch) {
	t.Helper()
	history, batch, err := f.live.RecordedHistoryWithAccountingBatch(f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	return history, batch
}

func newPendingAuxiliaryFullSave(t *testing.T) (*beforeFixture, []db.SessionHistoryBatch, db.SessionAccountingBatch) {
	t.Helper()
	f, _ := newAuxiliaryPersistFixture(t, true)
	settleRewindTurn(t, f, "establish all-set session")
	if err := f.live.RunRecordedTurn(t.Context(), "pending all four sets", nil, f.store, f.ui.SessionIdentity()); err != nil {
		t.Fatal(err)
	}
	// Apply real receipt/terminal messages without executing the returned save
	// command, so the public synchronous SaveSession owns the next persistence.
	f.applyEvents()
	history, batch := pendingAuxiliaryFullBatch(t, f)
	if len(history) == 0 || len(batch.Roots) == 0 || len(batch.Children) == 0 || len(batch.Auxiliaries) == 0 {
		t.Fatalf("fixture must retain all four sets: history=%d root=%d child=%d aux=%d", len(history), len(batch.Roots), len(batch.Children), len(batch.Auxiliaries))
	}
	return f, history, batch
}

func requirePendingAuxiliaryFullBatch(t *testing.T, f *beforeFixture, history []db.SessionHistoryBatch, batch db.SessionAccountingBatch) {
	t.Helper()
	gotHistory, got := pendingAuxiliaryFullBatch(t, f)
	if !reflect.DeepEqual(history, gotHistory) || !reflect.DeepEqual(batch, got) {
		t.Fatal("save acknowledged or changed pending history/root/child/auxiliary bytes")
	}
}

func TestAuxiliaryAccountingCancelledSaveSessionRetainsAllSetsAndRetries(t *testing.T) {
	f, history, batch := newPendingAuxiliaryFullSave(t)
	id := f.ui.SessionIdentity()
	before, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	roots, children, auxiliaries := durableProviderAccounting(t, f, id), durableChildren(t, f.store, id), durableAuxiliaries(t, f.store, id)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.ui.SaveSession(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SaveSession = %v", err)
	}
	requirePendingAuxiliaryFullBatch(t, f, history, batch)
	after, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(roots, durableProviderAccounting(t, f, id)) || !reflect.DeepEqual(children, durableChildren(t, f.store, id)) || !reflect.DeepEqual(auxiliaries, durableAuxiliaries(t, f.store, id)) {
		t.Fatal("cancelled save changed durable state/evidence", err)
	}
	if err := f.ui.SaveSession(t.Context()); err != nil {
		t.Fatal("fresh-context retry", err)
	}
	pendingHistory, pending := pendingAuxiliaryFullBatch(t, f)
	if len(pendingHistory) != 0 || len(pending.Roots) != 0 || len(pending.Children) != 0 || len(pending.Auxiliaries) != 0 {
		t.Fatal("successful retry did not acknowledge all four sets")
	}
	for _, row := range batch.Roots {
		saved, err := f.store.GetSessionProviderAccounting(t.Context(), id, row.RequestBatchID)
		if err != nil || !reflect.DeepEqual(saved.Accounting, row) {
			t.Fatal("retry changed root bytes", err)
		}
	}
	for _, row := range batch.Children {
		saved, err := f.store.GetSessionChildProviderAccounting(t.Context(), id, row.ChildTaskID, row.Attempt)
		if err != nil || !reflect.DeepEqual(saved.Accounting, row) {
			t.Fatal("retry changed child bytes", err)
		}
	}
	for _, row := range batch.Auxiliaries {
		saved, err := f.store.GetSessionAuxiliaryProviderAccounting(t.Context(), id, row.InvocationID)
		if err != nil || !reflect.DeepEqual(saved.Accounting, row) {
			t.Fatal("retry changed auxiliary bytes", err)
		}
	}
}

func TestAuxiliaryAccountingStaleSaveSessionRetainsAllSetsAndForeignVersion(t *testing.T) {
	f, history, batch := newPendingAuxiliaryFullSave(t)
	id := f.ui.SessionIdentity()
	// A separate Store is an actual competing writer, not a fabricated UI field.
	var path string
	if err := f.store.DB().QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	foreign, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	if err := foreign.RenameSession(t.Context(), id, "foreign writer version"); err != nil {
		t.Fatal(err)
	}
	version, err := foreign.SessionVersion(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	before, err := foreign.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	roots, children, auxiliaries := durableProviderAccounting(t, f, id), durableChildren(t, f.store, id), durableAuxiliaries(t, f.store, id)
	for range 2 {
		if err := f.ui.SaveSession(t.Context()); !errors.Is(err, db.ErrCheckpointConflict) {
			t.Fatalf("stale SaveSession = %v", err)
		}
		requirePendingAuxiliaryFullBatch(t, f, history, batch)
		afterVersion, err := foreign.SessionVersion(t.Context(), id)
		if err != nil || afterVersion != version {
			t.Fatal("stale save overwrote foreign version", err)
		}
		after, err := foreign.GetSessionResumeState(t.Context(), id)
		if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(roots, durableProviderAccounting(t, f, id)) || !reflect.DeepEqual(children, durableChildren(t, f.store, id)) || !reflect.DeepEqual(auxiliaries, durableAuxiliaries(t, f.store, id)) {
			t.Fatal("stale save changed foreign state or evidence", err)
		}
	}
}
