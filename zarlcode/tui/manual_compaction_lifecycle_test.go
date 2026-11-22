package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func manualTUIFixture(t *testing.T, onCall func(context.Context)) *beforeFixture {
	t.Helper()
	return manualTUIFixtureWithApplications(t, onCall, false)
}

func manualTUIFixtureWithApplications(t *testing.T, onCall func(context.Context), application bool) *beforeFixture {
	t.Helper()
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	f.ui.SetStartupReady(true)
	settleRewindTurn(t, f, "establish durable manual parent")
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyCompactEngine, "summary"); err != nil {
		t.Fatal(err)
	}
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyCompactProvider, prefs.ActiveModelTarget); err != nil {
		t.Fatal(err)
	}
	if err := f.live.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if application {
		f.sink.Close()
		f.applications = make(chan teasink.ApplicationDelivery, 1)
		f.sink = teasink.NewApplicationSink(func(msg tea.Msg) { f.applications <- msg.(teasink.ApplicationDelivery) })
		t.Cleanup(f.sink.Close)
		f.ui.SetLiveEventSink(f.sink)
	}
	provider := &accountingPersistProvider{onCall: onCall}
	live := engine.NewLiveRunner(provider, f.ws, "saved-model", engine.WithLiveSink(f.sink), engine.WithSettings(settings))
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	if application {
		t.Cleanup(func() {
			if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
				t.Error(err)
			}
		})
	}
	var history []llm.Message
	for range 20 {
		history = append(history, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("old prompt ", 20)}, llm.Message{Role: llm.RoleAssistant, Content: "old answer"})
	}
	live.RestoreContext(history)
	f.live = live
	f.ui.SetLiveRunner(live)
	f.ui.SetSettings(settings)
	return f
}

func TestManualCompactionTUIRecordedFullSaveAndReservation(t *testing.T) {
	calls := 0
	f := manualTUIFixture(t, func(context.Context) { calls++ })
	id := f.ui.SessionIdentity()
	before, err := f.store.GetSessionTranscript(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	command := f.ui.CompactNow()
	if command == nil {
		t.Fatal("missing manual command")
	}
	if _, err := f.live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("manual reservation absent: %v", err)
	}
	blocked := f.ui.Submit("must not start during manual compaction")
	if calls != 0 {
		t.Fatal("provider ran before command")
	}
	_ = blocked
	result := command()
	f.applyEvents()
	_, save := f.ui.Update(result)
	driveBeforeCommand(f, save)
	if calls == 0 {
		t.Fatalf("manual compactor was not invoked: result=%T toast=%s context=%d", result, f.ui.ToastText(), len(f.live.ContextSnapshot()))
	}
	rows, err := f.store.ListSessionManualProviderAccounting(t.Context(), id, 0, 100)
	if err != nil || len(rows) == 0 {
		t.Fatalf("manual rows=%d: %v", len(rows), err)
	}
	after, err := f.store.GetSessionTranscript(t.Context(), id)
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("manual save changed transcript revision: %d -> %d: %v", before.Revision, after.Revision, err)
	}
	reservation, err := f.live.ReserveTurn()
	if err != nil {
		t.Fatalf("manual reservation retained after ACK: %v", err)
	}
	reservation.Release()
}

func TestManualCompactionTUIUnstartedShutdownDoesNotDispatch(t *testing.T) {
	calls := 0
	f := manualTUIFixture(t, func(context.Context) { calls++ })
	command := f.ui.CompactNow()
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if msg := command(); msg != nil {
		t.Fatalf("shutdown-claimed command returned %T", msg)
	}
	if calls != 0 {
		t.Fatalf("unstarted helper dispatched %d times", calls)
	}
	reservation, err := f.live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
}

func TestManualCompactionTUIFailedSaveRetriesStorageOnly(t *testing.T) {
	calls := 0
	f := manualTUIFixture(t, func(context.Context) { calls++ })
	if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_manual BEFORE INSERT ON session_manual_provider_accounting BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	result := f.ui.CompactNow()()
	f.applyEvents()
	_, save := f.ui.Update(result)
	driveBeforeCommand(f, save)
	if calls == 0 {
		t.Fatalf("helper did not run: result=%T toast=%s context=%d", result, f.ui.ToastText(), len(f.live.ContextSnapshot()))
	}
	initial := calls
	if _, err := f.live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("failed-save barrier released: %v", err)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_manual`); err != nil {
		t.Fatal(err)
	}
	driveBeforeCommand(f, f.ui.RetryManualCompactionSave())
	if calls != initial {
		t.Fatalf("storage retry dispatched helper again: %d -> %d", initial, calls)
	}
	rows, err := f.store.ListSessionManualProviderAccounting(t.Context(), f.ui.SessionIdentity(), 0, 100)
	if err != nil || len(rows) == 0 {
		t.Fatalf("retry rows=%d: %v", len(rows), err)
	}
	reservation, err := f.live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
}
