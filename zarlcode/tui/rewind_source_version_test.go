package tui_test

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRewindRejectsSourceContentChangedAfterPreview(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "first")
	id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil {
		t.Fatal(err)
	}
	previewRewindPrompt(f, 1)
	competing, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	competing.PendingJSON = []byte(`[{"text":"other process draft"}]`)
	if err := f.store.SaveSessionDraft(t.Context(), competing); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	after, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rewind overwrote competing source content")
	}
	active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || active != id {
		t.Fatalf("rejected rewind activated child: %v", err)
	}
	if err := f.ui.SaveSession(t.Context()); err == nil {
		t.Fatal("preview conflict allowed later source overwrite")
	}
}

func TestBeforeRejectsSourceContentChangedWhileQueued(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(map[bool]string{false: "settled", true: "initial"}[initial], func(t *testing.T) {
			f := newBeforeFixture(t)
			f.ui.SetStartupReady(true)
			f.provider.check = func(context.Context) {}
			var id string
			if initial {
				_, debounce := f.ui.Update(tea.PasteMsg{Content: "initial draft"})
				driveBeforeCommand(f, debounce)
				sessions, err := f.store.ListSessions(t.Context(), f.ws.Root())
				if err != nil || len(sessions) != 1 {
					t.Fatalf("initial draft: %v", err)
				}
				id = sessions[0].ID
			} else {
				settleRewindTurn(t, f, "first")
				var err error
				id, err = f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
				if err != nil {
					t.Fatal(err)
				}
			}
			f.provider.check = func(context.Context) { t.Error("dispatched over competing source content") }
			cmd := f.ui.Submit("protected prompt")
			competing, err := f.store.GetSession(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			competing.PendingJSON = []byte(`[{"text":"other process draft"}]`)
			if err := f.store.SaveSessionDraft(t.Context(), competing); err != nil {
				t.Fatal(err)
			}
			before, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			cmd()
			f.applyEvents()
			after, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("BEFORE overwrote competing source content")
			}
			// API Submit does not clear a pre-existing composer draft. Preserve
			// both texts rather than treating the older local draft as lost input.
			wantPrompt := "protected prompt"
			if initial {
				wantPrompt += "\n\ninitial draft"
			}
			r, err := f.live.ReserveRuntime()
			if err != nil {
				t.Fatal(err)
			}
			r.Release()
			if f.ui.ComposerText() != wantPrompt {
				t.Fatal("rejected BEFORE lost prompt")
			}
			_, debounce := f.ui.Update(tea.PasteMsg{Content: " retained edit"})
			driveBeforeCommand(f, debounce)
			driveBeforeCommand(f, f.ui.Submit("must not dispatch"))
			if err := f.ui.SaveSession(t.Context()); err == nil {
				t.Fatal("conflicted session allowed a direct save")
			}
			after, err = f.store.GetSessionResumeState(t.Context(), id)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("subsequent save overwrote competing content: %v", err)
			}
		})
	}
}
