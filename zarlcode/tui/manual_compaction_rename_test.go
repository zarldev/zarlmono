package tui_test

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestManualCompactionTUIRenameCannotInvalidateRetry(t *testing.T) {
	calls := 0
	f := manualTUIFixture(t, func(context.Context) { calls++ })
	before, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	failManualSave(t, f)
	result := f.ui.CompactNow()()
	f.applyEvents()
	_, save := f.ui.Update(result)
	driveBeforeCommand(f, save)
	initial := calls
	f.ui.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	for range 80 {
		f.ui.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	f.ui.Update(tea.PasteMsg{Content: "rename during unsaved manual operation"})
	_, rename := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	driveBeforeCommand(f, rename)
	allowManualSave(t, f)
	driveBeforeCommand(f, f.ui.RetryManualCompactionSave())
	assertManualSavedAndReleased(t, f, initial, calls)
	after, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
	if err != nil || after.Label != before.Label {
		t.Fatalf("rename crossed reserved session: %q: %v", after.Label, err)
	}
}
