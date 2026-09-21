package tui_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestBeforeBoundaryAfterFailedTurnAndResume(t *testing.T) {
	for _, restart := range []bool{false, true} {
		name := "live"
		if restart {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) {
			f := newBeforeFixture(t)
			f.ui.SetStartupReady(true)
			f.provider.check = func(context.Context) {}
			f.provider.err = errors.New("terminal provider error")
			driveBeforeCommand(f, f.ui.Submit("first"))
			f.sink.Drain()
			f.mu.Lock()
			events := f.events
			f.events = nil
			f.mu.Unlock()
			for i, msg := range events {
				_, cmd := f.ui.Update(msg)
				if i == len(events)-1 {
					driveBeforeCommand(f, cmd)
				}
			}
			id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil {
				t.Fatal(err)
			}
			stored, err := f.store.GetSessionTranscript(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			var turnID string
			for _, entry := range stored.Entries {
				if entry.Kind == transcript.EntryKinds.ENTRYASSISTANTMESSAGE.String() && entry.ParentID == "" {
					turnID = entry.TurnID
				}
			}
			if turnID == "" {
				t.Fatal("missing settled turn")
			}
			if restart {
				if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			f.provider.err = nil
			called := false
			f.provider.check = func(ctx context.Context) {
				called = true
				records, err := f.store.ListSessionCheckpoints(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				for _, metadata := range records {
					record, err := f.store.GetSessionCheckpoint(ctx, id, metadata.ID)
					if err != nil {
						t.Fatal(err)
					}
					checkpoint, err := rewind.Load(ctx, f.store, record)
					if err != nil {
						t.Fatal(err)
					}
					snapshot, err := checkpoint.Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					if snapshot.Boundary.PromptText == "next" {
						if snapshot.Boundary.SettledTurnID != turnID {
							t.Fatalf("settled turn = %q, want %q", snapshot.Boundary.SettledTurnID, turnID)
						}
						return
					}
				}
				t.Fatal("next BEFORE missing")
			}
			driveBeforeCommand(f, f.ui.Submit("next"))
			if !called {
				t.Fatalf("next turn refused: %s", f.ui.ToastText())
			}
		})
	}
}
