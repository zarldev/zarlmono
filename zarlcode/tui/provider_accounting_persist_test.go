package tui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

type accountingPersistProvider struct{ onCall func(context.Context) }

func (*accountingPersistProvider) Name() string { return "openai" }
func (p *accountingPersistProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	p.onCall(ctx)
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "answer", UsageReported: true, Usage: llm.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
			PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 7, Known: true}, CacheRead: llm.TokenQuantity{Known: true}, CacheWrite: llm.TokenQuantity{Known: true}, Other: llm.TokenQuantity{Known: true}, PartitionKnown: true}, Output: llm.PricingOutputUsage{Total: llm.TokenQuantity{Tokens: 3, Known: true}, Other: llm.TokenQuantity{Known: true}}},
			Execution:    &llm.ExecutionObservation{GatewayCallID: "do-not-store-call", ReportedCost: llm.ReportedCost{Amount: 42, Currency: "JPY", Reported: true, Source: "do-not-store-source"}}, FinishReason: llm.FinishReasons.STOP}, nil)
	}
}

func newAccountingPersistenceFixture(t *testing.T) (*beforeFixture, *accountingPersistProvider) {
	t.Helper()
	f := newBeforeFixture(t)
	p := &accountingPersistProvider{onCall: func(context.Context) {}}
	f.live.ApplyTarget(engine.TargetUpdate{Provider: p, Spec: engine.ProviderSpec{Name: "openai", Model: "saved-model"}, RateCard: &llm.RateCard{
		Reference: llm.RateCardReference{ID: "frozen-policy", Revision: "v1"}, Currency: "EUR", Billing: llm.BillingModes.BILLINGTOKENMETERED, Applicable: true, TokenOnly: true,
		FreshInput: llm.TokenRate{PerMillionTokens: 2, Known: true}, Output: llm.TokenRate{PerMillionTokens: 3, Known: true},
	}})
	f.ui.SetStartupReady(true)
	return f, p
}

func pendingProviderAccounting(t *testing.T, f *beforeFixture) []db.SessionProviderAccounting {
	t.Helper()
	_, rows, err := f.live.RecordedHistoryWithProviderAccounting(f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func durableProviderAccounting(t *testing.T, f *beforeFixture, id string) []db.SessionProviderAccountingRecord {
	t.Helper()
	rows, err := f.store.ListSessionProviderAccounting(t.Context(), id, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestProviderAccountingFIFOFailureRetryAndStaleAcknowledgment(t *testing.T) {
	f, p := newAccountingPersistenceFixture(t)
	calls := 0
	p.onCall = func(ctx context.Context) {
		calls++
		if calls == 1 {
			if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_accounting BEFORE INSERT ON session_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected accounting failure'); END`); err != nil {
				t.Error(err)
			}
		}
	}
	settleRewindTurn(t, f, "first failed atomic full save")
	id := f.ui.SessionIdentity()
	if rows := durableProviderAccounting(t, f, id); len(rows) != 0 {
		t.Fatal("failed commit persisted accounting")
	}
	first := pendingProviderAccounting(t, f)
	if len(first) != 1 {
		t.Fatalf("pending rows=%d", len(first))
	}
	before, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	settleRewindTurn(t, f, "second memory-only invocation")
	frozen := pendingProviderAccounting(t, f)
	if len(frozen) != 2 || !bytes.Equal(frozen[0].PayloadJSON, first[0].PayloadJSON) {
		t.Fatal("later invocation replaced pending evidence")
	}
	openRecovery(f.ui)
	retry := recoveryKey(f.ui, 'r')
	if retry == nil {
		t.Fatal("missing failed-save recovery")
	}
	failedAck := retry()
	f.ui.Update(failedAck)
	after, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("accounting failure did not roll back full content", err)
	}
	if !reflect.DeepEqual(frozen, pendingProviderAccounting(t, f)) {
		t.Fatal("failed retry changed immutable accounting")
	}
	if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_accounting"); err != nil {
		t.Fatal(err)
	}
	retry = recoveryKey(f.ui, 'r')
	if retry == nil {
		t.Fatal("missing second recovery attempt")
	}
	f.ui.Update(failedAck) // stale UI ACK must not acknowledge the new operation
	if !reflect.DeepEqual(frozen, pendingProviderAccounting(t, f)) {
		t.Fatal("stale UI ACK dropped pending accounting")
	}
	committedAck := retry()
	rows := durableProviderAccounting(t, f, id)
	if len(rows) != 2 || calls != 2 {
		t.Fatalf("durable rows=%d provider calls=%d", len(rows), calls)
	}
	for i, row := range rows {
		if !reflect.DeepEqual(row.Accounting, frozen[i]) {
			t.Fatal("retry serialized or repriced accounting")
		}
	}
	if len(pendingProviderAccounting(t, f)) != 0 {
		t.Fatal("successful transaction did not ACK included rows")
	}
	f.ui.Update(committedAck)
	f.ui.Update(failedAck)
	requireRecoveredContext(t, f, "")
	if err := f.ui.SaveSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(durableProviderAccounting(t, f, id)) != 2 {
		t.Fatal("full-save retry duplicated accounting")
	}
	record, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var legacy tui.SessionUsageSnapshot
	if err := json.Unmarshal(record.LastUsageJSON, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.In != 14 || legacy.Out != 6 || legacy.CostUSD != 0 || legacy.CostParentUSD != 0 || legacy.Turns != 2 {
		t.Fatalf("dimensional accounting changed legacy rollup: %+v", legacy)
	}
	transcript, err := f.store.GetSessionTranscript(t.Context(), id)
	if err != nil || transcript.FormatVersion != 2 {
		t.Fatal("transcript format changed", err)
	}
	if bytes.Contains(record.LastUsageJSON, []byte("JPY")) || bytes.Contains(record.LastUsageJSON, []byte("frozen-policy")) {
		t.Fatal("accounting leaked into legacy totals")
	}
}

func TestProviderAccountingBeforeIncludesPriorPendingRows(t *testing.T) {
	f, p := newAccountingPersistenceFixture(t)
	settleRewindTurn(t, f, "establish exact session")
	id := f.ui.SessionIdentity()
	// Exercise the pending-owner contract via a completed recorded engine turn,
	// before the host has persisted/acknowledged it. Keep the UI at its last
	// settled boundary; the subsequent BEFORE must save every pending row.
	if err := f.live.RunRecordedTurn(t.Context(), "prior settled engine invocation", nil, f.store, id); err != nil {
		t.Fatal(err)
	}
	f.sink.Drain()
	f.mu.Lock()
	f.events = nil
	f.mu.Unlock()
	prior := pendingProviderAccounting(t, f)
	if len(prior) != 1 {
		t.Fatal("missing prior pending row")
	}
	// A failed BEFORE must retain those same bytes and must not dispatch a
	// future invocation. Retrying builds another Full snapshot of that owner.
	if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_before_accounting BEFORE INSERT ON session_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected BEFORE accounting failure'); END`); err != nil {
		t.Fatal(err)
	}
	p.onCall = func(context.Context) { t.Error("failed BEFORE dispatched provider") }
	driveBeforeCommand(f, f.ui.Submit("BEFORE that rolls back"))
	f.applyEvents()
	if !reflect.DeepEqual(prior, pendingProviderAccounting(t, f)) {
		t.Fatal("failed BEFORE acknowledged pending rows")
	}
	if rows := durableProviderAccounting(t, f, id); len(rows) != 1 {
		t.Fatal("failed BEFORE wrote accounting")
	}
	if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_before_accounting"); err != nil {
		t.Fatal(err)
	}
	p.onCall = func(ctx context.Context) {
		row, err := f.store.GetSessionProviderAccounting(ctx, id, prior[0].RequestBatchID)
		if err != nil || !reflect.DeepEqual(row.Accounting, prior[0]) {
			t.Error("BEFORE did not atomically include prior accounting", err)
		}
		if len(pendingProviderAccounting(t, f)) != 0 {
			t.Error("BEFORE did not acknowledge prior row")
		}
	}
	settleRewindTurn(t, f, "next BEFORE and dispatch")
	if rows := durableProviderAccounting(t, f, id); len(rows) != 3 {
		t.Fatalf("BEFORE/terminal rows=%d", len(rows))
	}
	if len(pendingProviderAccounting(t, f)) != 0 {
		t.Fatal("terminal full save retained committed row")
	}
}

func TestProviderAccountingResumeAndForkDoNotCopyCharges(t *testing.T) {
	f, _ := newAccountingPersistenceFixture(t)
	settleRewindTurn(t, f, "same prompt")
	settleRewindTurn(t, f, "same prompt")
	id := f.ui.SessionIdentity()
	original := durableProviderAccounting(t, f, id)
	if len(original) != 2 {
		t.Fatal("missing source accounting")
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
	if err != nil || !bytes.Equal(record.LastUsageJSON, legacy) {
		t.Fatal("resume changed legacy USD or usage", err)
	}
	if !reflect.DeepEqual(original, durableProviderAccounting(t, f, id)) {
		t.Fatal("resume repriced or changed accounting")
	}
	previewRewindPrompt(f, 1)
	_, apply := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	child := f.ui.SessionIdentity()
	if child == id {
		t.Fatal("rewind did not fork", f.ui.ToastText())
	}
	if rows := durableProviderAccounting(t, f, child); len(rows) != 0 {
		t.Fatal("fork copied source charges")
	}
	driveBeforeCommand(f, apply)
	// Child accounting starts only with its own newly prepared request receipt.
	settleRewindTurn(t, f, "new child invocation")
	rows := durableProviderAccounting(t, f, child)
	if len(rows) != 1 {
		t.Fatalf("child rows=%d", len(rows))
	}
	for _, source := range original {
		if rows[0].Accounting.RequestBatchID == source.Accounting.RequestBatchID {
			t.Fatal("child reused source request receipt")
		}
	}
	if !reflect.DeepEqual(original, durableProviderAccounting(t, f, id)) {
		t.Fatal("fork altered original charges")
	}
	if _, err := engine.ReadRecordedProviderAccounting(t.Context(), f.store, child, original[0].Accounting.RequestBatchID); !errors.Is(err, db.ErrNotFound) {
		t.Fatal("child can read source charge", err)
	}
}
