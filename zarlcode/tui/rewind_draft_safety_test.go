package tui_test

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestFreshSourceSettlementFailureRetainsProtectedDraft(t *testing.T) {
	f := newBeforeFixture(t)
	var before db.SessionResumeState
	f.provider.check = func(ctx context.Context) {
		id, err := f.store.GetSettingExact(ctx, f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		before, err = f.store.GetSessionResumeState(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_fresh_settlement BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
			t.Fatal(err)
		}
	}
	settleRewindTurn(t, f, "fresh protected prompt")
	if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_fresh_settlement"); err != nil {
		t.Fatal(err)
	}
	// Neither an empty composer clear nor a new draft may replace recovery input
	// until settlement commits the matching canonical/context pair.
	_, debounce := f.ui.Update(tea.PasteMsg{Content: "new draft"})
	driveBeforeCommand(f, debounce)
	f.ui.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if err := f.ui.FlushSessionPersistence(t.Context()); err == nil {
		t.Fatal("shutdown hid the unsaved turn")
	}
	after, err := f.store.GetSessionResumeState(t.Context(), before.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	text, err := draft.Decode(after.Session.PendingJSON)
	if err != nil || text != "fresh protected prompt" || !reflect.DeepEqual(before.Transcript, after.Transcript) || !reflect.DeepEqual(before.Session.ContextJSON, after.Session.ContextJSON) {
		t.Fatalf("failed fresh settlement lost paired recovery head/draft: %q, %v", text, err)
	}
}
