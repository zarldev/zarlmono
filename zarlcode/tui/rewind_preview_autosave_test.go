package tui_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
)

func TestRewindPreviewHoldsDraftAutosave(t *testing.T) {
	for _, mode := range []string{"apply", "cancel", "failed-apply-cancel"} {
		t.Run(mode, func(t *testing.T) {
			cancel := mode != "apply"
			f := newBeforeFixture(t)
			f.ui.SetStartupReady(true)
			f.provider.check = func(context.Context) {}
			settleRewindTurn(t, f, "selected prompt")
			id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			_, debounce := f.ui.Update(tea.PasteMsg{Content: "source draft"})
			previewRewindPrompt(f, 1)
			if strings.Contains(f.ui.View().Content, "Unavailable:") {
				t.Fatal("preview not actionable")
			}
			if mode == "failed-apply-cancel" {
				if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_preview_save BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
				f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if !strings.Contains(f.ui.View().Content, "Unavailable:") {
					t.Fatal("failed apply did not leave preview open")
				}
				if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_preview_save"); err != nil {
					t.Fatal(err)
				}
			}
			// Deliver a previously scheduled debounce only after preview captured
			// its token. It must not write locally and stale that token.
			driveBeforeCommand(f, debounce)
			during, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil || !reflect.DeepEqual(before, during) {
				t.Fatalf("autosave changed preview source: %v", err)
			}
			key := tea.KeyEnter
			if cancel {
				key = tea.KeyEscape
			}
			_, closeCmd := f.ui.Update(tea.KeyPressMsg{Code: key})
			driveBeforeCommand(f, closeCmd)
			active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil || (active == id) != cancel {
				t.Fatalf("unexpected active branch: %v", err)
			}
			after, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			text, err := draft.Decode(after.Session.PendingJSON)
			if err != nil || text != "source draft" {
				t.Fatalf("source draft not preserved: %v", err)
			}
			if !reflect.DeepEqual(before.Transcript, after.Transcript) {
				t.Fatal("original transcript changed")
			}
			if !cancel && f.ui.ComposerText() != "selected prompt" {
				t.Fatal("selected prompt not prefilled")
			}
		})
	}
}
