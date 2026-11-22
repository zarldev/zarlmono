package tui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
)

func TestManualCompactionTUISnapshotFailureCanRetryWithoutInference(t *testing.T) {
	calls := 0
	f := manualTUIFixture(t, func(context.Context) { calls++ })
	command := f.ui.CompactNow()
	f.ui.Update(tea.PasteMsg{Content: strings.Repeat("x", draft.MaxTextBytes+1)})
	result := command()
	f.applyEvents()
	_, save := f.ui.Update(result)
	driveBeforeCommand(f, save)
	initial := calls
	if initial == 0 || !strings.Contains(f.ui.ToastText(), "compact save") {
		t.Fatalf("snapshot failure not reached: calls=%d toast=%s", calls, f.ui.ToastText())
	}
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if len(f.ui.ComposerText()) != draft.MaxTextBytes {
		t.Fatal("oversized draft not repaired")
	}
	retry := f.ui.RetryManualCompactionSave()
	if retry == nil {
		t.Fatal("snapshot failure has no recovery command")
	}
	driveBeforeCommand(f, retry)
	assertManualSavedAndReleased(t, f, initial, calls)
}

func TestManualCompactionTUIRetryPreservesLaterDraft(t *testing.T) {
	calls := 0
	f := manualTUIFixture(t, func(context.Context) { calls++ })
	failManualSave(t, f)
	result := f.ui.CompactNow()()
	f.applyEvents()
	_, save := f.ui.Update(result)
	driveBeforeCommand(f, save)
	initial := calls
	_, debounce := f.ui.Update(tea.PasteMsg{Content: "draft written after failed compaction save"})
	driveBeforeCommand(f, debounce)
	allowManualSave(t, f)
	f.ui.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	_, retry := f.ui.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if retry == nil {
		t.Fatal("recovery dialog did not expose manual save retry")
	}
	driveBeforeCommand(f, retry)
	assertManualSavedAndReleased(t, f, initial, calls)
	saved, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	text, err := draft.Decode(saved.PendingJSON)
	if err != nil || text != f.ui.ComposerText() {
		t.Fatalf("later draft lost: %q: %v", text, err)
	}
}

func TestManualCompactionTUIShutdownRetryCanResumeAfterStorageRecovery(t *testing.T) {
	calls := 0
	f := manualTUIFixture(t, func(context.Context) { calls++ })
	appCtx, cancelApp := context.WithCancel(t.Context())
	t.Cleanup(cancelApp)
	f.ui.SetApplicationContext(appCtx)
	failManualSave(t, f)
	result := f.ui.CompactNow()()
	f.applyEvents()
	_, save := f.ui.Update(result)
	driveBeforeCommand(f, save)
	initial := calls
	_, debounce := f.ui.Update(tea.PasteMsg{Content: "deferred draft at shutdown"})
	driveBeforeCommand(f, debounce)
	cancelApp()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := f.ui.FlushSessionPersistence(ctx); err == nil {
		t.Fatal("shutdown reported failed storage as durable")
	}
	allowManualSave(t, f)
	retryCtx, retryCancel := context.WithTimeout(t.Context(), time.Second)
	defer retryCancel()
	if err := f.ui.FlushSessionPersistence(retryCtx); err != nil {
		t.Fatalf("second shutdown cannot recover: %v", err)
	}
	assertManualSavedAndReleased(t, f, initial, calls)
	saved, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	text, err := draft.Decode(saved.PendingJSON)
	if err != nil || text != "deferred draft at shutdown" {
		t.Fatalf("shutdown lost deferred draft: %q: %v", text, err)
	}
}

func failManualSave(t *testing.T, f *beforeFixture) {
	t.Helper()
	if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_manual_recovery BEFORE INSERT ON session_manual_provider_accounting BEGIN SELECT RAISE(ABORT,'injected manual save failure'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.store.DB().ExecContext(context.WithoutCancel(t.Context()), `DROP TRIGGER IF EXISTS reject_manual_recovery`)
	})
}

func allowManualSave(t *testing.T, f *beforeFixture) {
	t.Helper()
	if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_manual_recovery`); err != nil {
		t.Fatal(err)
	}
}

func assertManualSavedAndReleased(t *testing.T, f *beforeFixture, initial, calls int) {
	t.Helper()
	if initial == 0 || calls != initial {
		t.Fatalf("retry dispatched inference: before=%d after=%d", initial, calls)
	}
	rows, err := f.store.ListSessionManualProviderAccounting(t.Context(), f.ui.SessionIdentity(), 0, 100)
	if err != nil || len(rows) == 0 {
		t.Fatalf("manual accounting not saved: %d rows: %v", len(rows), err)
	}
	reservation, err := f.live.ReserveTurn()
	if err != nil {
		t.Fatalf("manual reservation retained: %v", err)
	}
	reservation.Release()
}
