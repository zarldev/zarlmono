package tui_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSettlementRejectsWriterAfterBeforeCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first")
		id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		var foreign db.SessionContentVersion
		f.provider.check = func(ctx context.Context) {
			record, err := f.store.GetSession(ctx, id)
			if err != nil {
				t.Error(err)
				return
			}
			record.PendingJSON, err = draft.Encode("foreign draft during model turn")
			if err != nil {
				t.Error(err)
				return
			}
			if err := f.store.SaveSessionDraft(ctx, record); err != nil {
				t.Error(err)
				return
			}
			foreign, err = f.store.SessionVersion(ctx, id)
			if err != nil {
				t.Error(err)
			}
		}
		settleRewindTurn(t, f, "second")
		after, err := f.store.SessionVersion(t.Context(), id)
		if err != nil || after != foreign {
			t.Fatalf("settlement overwrote competing row: %v", err)
		}
		if err := f.ui.SaveSession(t.Context()); !errors.Is(err, db.ErrCheckpointConflict) {
			t.Fatalf("save after settlement conflict = %v", err)
		}
	})
}

func TestShutdownRejectsWriterAfterLastReceipt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first")
		id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		record, err := f.store.GetSession(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		record.PendingJSON, err = draft.Encode("foreign draft before shutdown")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SaveSessionDraft(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		before, err := f.store.GetSessionResumeState(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.ui.FlushSessionPersistence(t.Context()); !errors.Is(err, db.ErrCheckpointConflict) {
			t.Fatalf("shutdown conflict = %v", err)
		}
		after, err := f.store.GetSessionResumeState(t.Context(), id)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("shutdown overwrote competing row: %v", err)
		}
	})
}

func TestQueuedDeleteRejectsCompetingWriter(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "command", true: "shutdown"}[shutdown], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newBeforeFixture(t)
				f.ui.SetStartupReady(true)
				f.provider.check = func(context.Context) {}
				settleRewindTurn(t, f, "first")
				id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
				if err != nil {
					t.Fatal(err)
				}
				f.ui.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
				f.ui.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
				_, remove := f.ui.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
				if remove == nil {
					t.Fatal("delete was not queued")
				}
				if err := f.store.RenameSession(t.Context(), id, "foreign rename"); err != nil {
					t.Fatal(err)
				}
				before, err := f.store.GetSessionResumeState(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				if shutdown {
					if err := f.ui.FlushSessionPersistence(t.Context()); !errors.Is(err, db.ErrCheckpointConflict) {
						t.Fatalf("delete conflict = %v", err)
					}
				} else {
					driveBeforeCommand(f, remove)
				}
				after, err := f.store.GetSessionResumeState(t.Context(), id)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("delete changed foreign state: %v", err)
				}
				active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
				if err != nil || active != id {
					t.Fatalf("delete conflict cleared active pointer: %v", err)
				}
			})
		})
	}
}

func TestLocalRenameReceiptAllowsShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first")
		driveBeforeCommand(f, f.ui.Submit("/name local title"))
		if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
			t.Fatal(err)
		}
		sessions, err := f.store.ListSessions(t.Context(), f.ws.Root())
		if err != nil || len(sessions) != 1 || sessions[0].Label != "local title" {
			t.Fatalf("local rename not retained: %v", err)
		}
	})
}

func TestResumeOwnershipRejectsLaterWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first")
		if err := f.ui.ResumeLatestSavedSession(t.Context()); err != nil {
			t.Fatal(err)
		}
		id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetSessionPinned(t.Context(), id, true, time.Now()); err != nil {
			t.Fatal(err)
		}
		before, err := f.store.GetSessionResumeState(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.ui.SaveSession(t.Context()); !errors.Is(err, db.ErrCheckpointConflict) {
			t.Fatalf("resumed save = %v", err)
		}
		after, err := f.store.GetSessionResumeState(t.Context(), id)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("resumed save changed foreign state: %v", err)
		}
	})
}

func TestBeforeReceiptCarriesQueuedRenameIntoSettlement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first")
		driveBeforeCommand(f, f.ui.Submit("second"))
		f.sink.Drain()
		f.mu.Lock()
		events := f.events
		f.events = nil
		f.mu.Unlock()
		for i, event := range events {
			_, cmd := f.ui.Update(event)
			if started, ok := event.(teasink.ConversationStartedMsg); ok && started.Depth == 0 {
				driveBeforeCommand(f, f.ui.Submit("/name local title"))
			}
			if i == len(events)-1 {
				driveBeforeCommand(f, cmd)
			}
		}
		if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
			t.Fatalf("BEFORE/rename/settlement chain: %v", err)
		}
		sessions, err := f.store.ListSessions(t.Context(), f.ws.Root())
		if err != nil || len(sessions) != 1 {
			t.Fatalf("sessions after rename: %v", err)
		}
		if sessions[0].Label != "local title" {
			t.Fatalf("rename lost at settlement: label=%q toast=%q", sessions[0].Label, f.ui.ToastText())
		}
	})
}

func TestShutdownAdvancesUnacknowledgedLocalReceipt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first")
		cmd := f.ui.ForceTranscriptPersist()
		if cmd == nil {
			t.Fatal("missing local full save")
		}
		cmd() // committed receipt, acknowledgement remains unread for shutdown
		if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
			t.Fatalf("shutdown did not advance local receipt: %v", err)
		}
	})
}

func TestQueuedExactSaveIgnoresChangedActiveSelection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first")
		cmd := f.ui.ForceTranscriptPersist()
		if cmd == nil {
			t.Fatal("missing exact save")
		}
		before, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", "foreign-selection"); err != nil {
			t.Fatal(err)
		}
		driveBeforeCommand(f, cmd)
		after, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("exact save changed otherwise unchanged source: %v", err)
		}
		active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if err != nil || active != "foreign-selection" {
			t.Fatalf("exact save reclaimed active selection: %v", err)
		}
		if err := f.ui.SaveSession(t.Context()); err != nil {
			t.Fatalf("workspace selection blocked subsequent save: %v", err)
		}
	})
}

func TestSessionRecoveryRetryCarriesAcknowledgedRename(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, calls := failedRecoveryFixture(t)
		removeRecoveryFailure(t, f)
		driveBeforeCommand(f, f.ui.Submit("/name retained local title"))
		record, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
		if err != nil || record.Label != "retained local title" {
			t.Fatalf("local rename not acknowledged: %v", err)
		}
		openRecovery(f.ui)
		cmd := recoveryKey(f.ui, 'r')
		if cmd == nil {
			t.Fatal("missing retry", f.ui.ToastText())
		}
		driveBeforeCommand(f, cmd)
		requireRecoveredContext(t, f, "")
		if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
			t.Fatalf("save after retry = %v", err)
		}
		record, err = f.store.GetSession(t.Context(), f.ui.SessionIdentity())
		if err != nil || record.Label != "retained local title" || *calls != 1 {
			t.Fatalf("retry lost rename or dispatched model: %v", err)
		}
	})
}

func TestSessionRecoveryShutdownSavesDraftAfterRetryCapture(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unstarted", true: "committed-unacknowledged"}[committed], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, calls := failedRecoveryFixture(t)
				removeRecoveryFailure(t, f)
				openRecovery(f.ui)
				cmd := recoveryKey(f.ui, 'r')
				if cmd == nil {
					t.Fatal("missing retry", f.ui.ToastText())
				}
				recoveryKey(f.ui, tea.KeyEscape)
				f.ui.Update(tea.PasteMsg{Content: "draft after retry capture"})
				if committed {
					cmd() // commit without delivering the acknowledgement
				}
				if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
					t.Fatal(err)
				}
				requireRecoveredContext(t, f, "draft after retry capture")
				if cmd() != nil || *calls != 1 {
					t.Fatal("shutdown repeated retry or invoked model")
				}
			})
		})
	}
}
