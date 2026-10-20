package tui_test

import (
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestFlushDraftOnlyFinalEdit(t *testing.T) {
	for _, mode := range []string{"debounce", "queued", "conflict", "rejected", "delete-current", "delete-other"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				root := t.TempDir()
				workspace, err := code.NewWorkspace(root)
				if err != nil {
					t.Fatal(err)
				}
				store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				pending := []byte(`{"text":"old"}`)
				if mode == "rejected" {
					pending = []byte(" {\n \"version\": 99, \"text\": \"preserve\"\n} ")
				}
				const id = "draft-only"
				if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{ID: id, Workspace: root, PendingJSON: pending}); err != nil {
					t.Fatal(err)
				}
				live := engine.NewLiveRunner(nil, workspace, "test-model")
				ui := tui.New()
				ui.SetLiveRunner(live)
				ui.SetSettings(engine.NewSettings(store, nil, nil, root))
				ui.SetStartupReady(true)
				if mode == "delete-other" {
					if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{ID: "other", Workspace: root, PendingJSON: pending}); err != nil {
						t.Fatal(err)
					}
					if err := ui.ResumeSavedSession(t.Context(), "other"); err != nil {
						t.Fatal(err)
					}
					ui.QueueDeletePersist("other")
				}
				if err := ui.ResumeSavedSession(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				var prior tea.Cmd
				if mode != "rejected" {
					_, debounce := ui.Update(tea.PasteMsg{Content: "-first"})
					if mode == "queued" {
						if debounce == nil {
							t.Fatal("missing debounce")
						}
						_, prior = ui.Update(debounce())
						if prior == nil {
							t.Fatal("missing queued write")
						}
					}
					ui.Update(tea.PasteMsg{Content: "-latest"})
				}
				if mode == "conflict" {
					pending = []byte(`{"text":"foreign"}`)
					if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{ID: id, Workspace: root, PendingJSON: pending}); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "delete-current" {
					ui.QueueDeletePersist(id)
				}
				err = ui.FlushSessionPersistence(t.Context())
				if mode == "conflict" {
					if !errors.Is(err, db.ErrCheckpointConflict) {
						t.Fatalf("flush = %v, want conflict", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if prior != nil && prior() != nil {
					t.Fatal("late command was not claimed by shutdown")
				}
				if mode == "delete-other" {
					if _, err := store.GetSession(t.Context(), "other"); !errors.Is(err, db.ErrNotFound) {
						t.Fatalf("other session after delete = %v, want not found", err)
					}
				}
				saved, err := store.GetSession(t.Context(), id)
				if mode == "delete-current" {
					if !errors.Is(err, db.ErrNotFound) {
						t.Fatalf("current session after delete = %v, want not found", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if mode == "rejected" || mode == "conflict" {
					if string(saved.PendingJSON) != string(pending) {
						t.Fatalf("protected bytes = %q, want %q", saved.PendingJSON, pending)
					}
				} else {
					text, err := draft.Decode(saved.PendingJSON)
					if err != nil || text != ui.ComposerText() {
						t.Fatalf("draft = %q, want %q: %v", text, ui.ComposerText(), err)
					}
				}
				if !ui.CanonicalThread().IsEmpty() || len(live.ContextSnapshot()) != 0 {
					t.Fatal("draft flush changed conversation")
				}
			})
		})
	}
}
