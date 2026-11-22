package tui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

type lateChildProvider struct {
	started chan struct{}
	partial bool
}

func (*lateChildProvider) Name() string { return "openai" }
func (p *lateChildProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	if taskscope.DepthFrom(ctx) == 0 {
		p.started = make(chan struct{})
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{ID: "same-provider-tool-id", Type: "function", Function: llm.ToolCallFunction{Name: "agent_spawn", Arguments: `{"prompt":"synthetic child prompt canary","mode":"explore"}`}}}, FinishReason: llm.FinishReasons.TOOLCALLS}, nil)
		}
	}
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(p.started)
		if p.partial {
			if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
				PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 7, Known: true}}},
				Execution:    &llm.ExecutionObservation{GatewayCallID: "synthetic-call-canary", ReportedCost: llm.ReportedCost{Amount: 42, Currency: "JPY", Reported: true, Source: "synthetic-source-canary"}}}, nil) {
				return
			}
		}
		<-ctx.Done()
		yield(llm.CompletionChunk{}, ctx.Err())
	}
}

type lateChildSink struct {
	*teasink.Sink
	provider  *lateChildProvider
	rootEnded atomic.Bool
	childLate atomic.Bool
}

func (s *lateChildSink) OnToolCompleted(ctx context.Context, e runner.ToolCompleted) {
	if e.Depth == 0 && e.ToolName == "agent_spawn" {
		select {
		case <-s.provider.started:
		case <-ctx.Done():
		}
	}
	s.Sink.OnToolCompleted(ctx, e)
}
func (s *lateChildSink) OnConversationEnded(ctx context.Context, e runner.ConversationEnded) {
	if e.Depth == 0 {
		s.rootEnded.Store(true)
	}
	s.Sink.OnConversationEnded(ctx, e)
}
func (s *lateChildSink) OnProviderAttemptSettled(ctx context.Context, e runner.ProviderAttemptSettled) {
	if e.Depth > 0 {
		s.childLate.Store(s.rootEnded.Load())
	}
	s.Sink.OnProviderAttemptSettled(ctx, e)
}

func newLateChildFixture(t *testing.T, partial bool) (*beforeFixture, *lateChildSink) {
	t.Helper()
	return configureLateChildFixture(t, newBeforeFixture(t), partial)
}

func configureLateChildFixture(t *testing.T, f *beforeFixture, partial bool) (*beforeFixture, *lateChildSink) {
	t.Helper()
	p := &lateChildProvider{started: make(chan struct{}), partial: partial}
	sink := &lateChildSink{Sink: f.sink, provider: p}
	live := engine.NewLiveRunner(p, f.ws, "saved-model", engine.WithReadOnlyTasks(), engine.WithLiveSink(sink))
	live.SetProviderSpec(p, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	live.SetLimits(1024, 1, 3, 1)
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
	return f, sink
}

func pendingChildren(t *testing.T, f *beforeFixture) []db.SessionChildProviderAccounting {
	t.Helper()
	_, _, rows, err := f.live.RecordedHistoryWithAccounting(f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func durableChildren(t *testing.T, store *db.Store, id string) []db.SessionChildProviderAccountingRecord {
	t.Helper()
	rows, err := store.ListSessionChildProviderAccounting(t.Context(), id, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestChildAccountingActualRuntimeDrainPersistsAfterRootTerminal(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "unreported cancelled", true: "partial cancelled"}[partial], func(t *testing.T) {
			f, sink := newLateChildFixture(t, partial)
			settleRewindTurn(t, f, "delegate before reaching root iteration limit")
			if !sink.childLate.Load() {
				t.Fatal("test did not settle child after root terminal")
			}
			id := f.ui.SessionIdentity()
			rows := durableChildren(t, f.store, id)
			if len(rows) != 1 || len(pendingChildren(t, f)) != 0 {
				t.Fatalf("late child durable=%d pending=%d", len(rows), len(pendingChildren(t, f)))
			}
			child, err := engine.ReadRecordedChildProviderAccounting(t.Context(), f.store, id, rows[0].Accounting.ChildTaskID, 1)
			if err != nil || child.OwnerRequestBatchID == "" || child.Provider != "openai" || child.Model != "saved-model" {
				t.Fatal("child identity", err)
			}
			if partial {
				if child.Usage == nil || child.Usage.TotalTokens != 10 || child.ReportedCost == nil || child.ReportedCost.Amount != 42 || child.ReportedCost.Source != "" {
					t.Fatal("lost partial evidence")
				}
			} else if child.Usage != nil || child.PricingUsage != nil || child.ReportedCost != nil || child.ConfiguredEstimate != nil {
				t.Fatal("missing evidence inferred zero")
			}
			for _, canary := range []string{"synthetic child prompt canary", "synthetic-call-canary", "synthetic-source-canary"} {
				if bytes.Contains(rows[0].Accounting.PayloadJSON, []byte(canary)) {
					t.Fatal("privacy canary persisted")
				}
			}
			legacy := f.ui.UsageSnapshot()
			if legacy.CostUSD != 0 || legacy.CostParentUSD != 0 {
				t.Fatal("reported cost booked as legacy USD")
			}
			// A second independently opened Store proves committed visibility and frozen
			// decoding rather than a transient callback or current-price recomputation.
			var path string
			if err := f.store.DB().QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&path); err != nil {
				t.Fatal(err)
			}
			if !filepath.IsAbs(path) {
				t.Fatal("fixture DB not absolute")
			}
			reopened, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			if !reflect.DeepEqual(rows, durableChildren(t, reopened, id)) {
				t.Fatal("reopen changed evidence")
			}
			if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rows, durableChildren(t, reopened, id)) {
				t.Fatal("shutdown duplicated/repriced evidence")
			}
		})
	}
}

func TestChildAccountingActualFullSaveFailureImmutableRetryAndBefore(t *testing.T) {
	f, sink := newLateChildFixture(t, true)
	if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_child BEFORE INSERT ON session_child_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected child failure'); END`); err != nil {
		t.Fatal(err)
	}
	settleRewindTurn(t, f, "failed late child save")
	id := f.ui.SessionIdentity()
	first := pendingChildren(t, f)
	if len(first) != 1 || !sink.childLate.Load() || len(durableChildren(t, f.store, id)) != 0 {
		t.Fatal("failure lost late child")
	}
	settleRewindTurn(t, f, "new child while earlier full save remains failed")
	newer := pendingChildren(t, f)
	if len(newer) != 2 || !reflect.DeepEqual(newer[0], first[0]) {
		t.Fatal("later child replaced earlier immutable retry bytes")
	}
	first = newer
	before, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	openRecovery(f.ui)
	retry := recoveryKey(f.ui, 'r')
	if retry == nil {
		t.Fatal("missing retry")
	}
	failedAck := retry()
	f.ui.Update(failedAck)
	after, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(first, pendingChildren(t, f)) {
		t.Fatal("failed retry mutated snapshot/state", err)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_child`); err != nil {
		t.Fatal(err)
	}
	retry = recoveryKey(f.ui, 'r')
	if retry == nil {
		t.Fatal("missing second retry")
	}
	f.ui.Update(failedAck)
	if !reflect.DeepEqual(first, pendingChildren(t, f)) {
		t.Fatal("stale UI ack released children")
	}
	f.ui.Update(retry())
	saved := durableChildren(t, f.store, id)
	if len(saved) != 2 || !reflect.DeepEqual(saved[0].Accounting, first[0]) || !reflect.DeepEqual(saved[1].Accounting, first[1]) || len(pendingChildren(t, f)) != 0 {
		t.Fatal("retry changed serialized bytes")
	}
	requireRecoveredContext(t, f, "")
	// A completed engine operation can leave evidence pending before the next
	// UI BEFORE. The BEFORE transaction must save/ACK both sets, not only roots.
	if err := f.live.RunRecordedTurn(t.Context(), "prior pending child", nil, f.store, id); err != nil {
		t.Fatal(err)
	}
	f.sink.Drain()
	f.mu.Lock()
	f.events = nil
	f.mu.Unlock()
	prior := pendingChildren(t, f)
	if len(prior) != 1 {
		t.Fatal("missing prior child")
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_before_child BEFORE INSERT ON session_child_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected BEFORE child failure'); END`); err != nil {
		t.Fatal(err)
	}
	driveBeforeCommand(f, f.ui.Submit("BEFORE must roll back"))
	f.applyEvents()
	if !reflect.DeepEqual(prior, pendingChildren(t, f)) || len(durableChildren(t, f.store, id)) != 2 {
		t.Fatal("failed BEFORE acknowledged child")
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_before_child`); err != nil {
		t.Fatal(err)
	}
	settleRewindTurn(t, f, "successful BEFORE plus late settlement")
	rows := durableChildren(t, f.store, id)
	if len(rows) != 4 || len(pendingChildren(t, f)) != 0 {
		t.Fatal("BEFORE/terminal child loss")
	}
	found := false
	for _, row := range rows {
		if reflect.DeepEqual(row.Accounting, prior[0]) {
			found = true
		}
	}
	if !found {
		t.Fatal("BEFORE repriced prior child")
	}
	if _, err := engine.ReadRecordedChildProviderAccounting(t.Context(), f.store, "another-session", prior[0].ChildTaskID, 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatal("child cross-session visibility", err)
	}
}

func TestChildAccountingNonexactFullSaveAndShutdown(t *testing.T) {
	f, _ := newLateChildFixture(t, true)
	settleRewindTurn(t, f, "establish recorded session")
	id := f.ui.SessionIdentity()
	// Resume the same valid transcript and retained receipt history through the
	// supported legacy context representation, without claiming an exact head.
	record, err := f.store.GetSession(t.Context(), id)
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
	if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := f.live.RunRecordedTurn(t.Context(), "ordinary recorded child", nil, f.store, id); err != nil {
		t.Fatal(err)
	}
	f.applyEvents()
	pending := pendingChildren(t, f)
	if len(pending) != 1 {
		t.Fatal("missing ordinary pending child")
	}
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved := durableChildren(t, f.store, id)
	if len(saved) != 2 || !reflect.DeepEqual(saved[1].Accounting, pending[0]) || len(pendingChildren(t, f)) != 0 {
		t.Fatal("ordinary full save lost/repriced child")
	}
	if err := f.live.RunRecordedTurn(t.Context(), "ordinary shutdown child", nil, f.store, id); err != nil {
		t.Fatal(err)
	}
	f.applyEvents()
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(durableChildren(t, f.store, id)) != 3 || len(pendingChildren(t, f)) != 0 {
		t.Fatal("ordinary shutdown lost child")
	}
}

func TestChildAccountingResumeAndRewindForkRetention(t *testing.T) {
	f, _ := newLateChildFixture(t, true)
	settleRewindTurn(t, f, "first recorded child")
	settleRewindTurn(t, f, "second recorded child")
	id := f.ui.SessionIdentity()
	original := durableChildren(t, f.store, id)
	if len(original) != 2 {
		t.Fatal("missing source child evidence")
	}
	record, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Clone(record.LastUsageJSON)
	if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := f.ui.SaveSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	record, err = f.store.GetSession(t.Context(), id)
	if err != nil || !bytes.Equal(record.LastUsageJSON, legacy) || !reflect.DeepEqual(original, durableChildren(t, f.store, id)) {
		t.Fatal("resume repriced evidence or changed legacy usage", err)
	}
	previewRewindPrompt(f, 1)
	_, apply := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	fork := f.ui.SessionIdentity()
	if fork == id {
		t.Fatal("rewind did not fork", f.ui.ToastText())
	}
	if len(durableChildren(t, f.store, fork)) != 0 {
		t.Fatal("fork copied incurred evidence")
	}
	driveBeforeCommand(f, apply)
	settleRewindTurn(t, f, "fork own recorded child")
	rows := durableChildren(t, f.store, fork)
	if len(rows) != 1 {
		t.Fatal("missing fork child")
	}
	for _, row := range original {
		if rows[0].Accounting.ChildTaskID == row.Accounting.ChildTaskID || rows[0].Accounting.OwnerRequestBatchID == row.Accounting.OwnerRequestBatchID {
			t.Fatal("fork reused source identity")
		}
	}
	if !reflect.DeepEqual(original, durableChildren(t, f.store, id)) {
		t.Fatal("rewind altered incurred source evidence")
	}
}
