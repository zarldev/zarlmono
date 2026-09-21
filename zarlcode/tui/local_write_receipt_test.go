package tui_test

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
)

func TestQueuedLocalWritesPreserveReplacementDraft(t *testing.T) {
	for _, preceding := range []string{"clear-established", "clear-draft-only", "full", "transcript"} {
		t.Run(preceding, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newBeforeFixture(t)
				f.ui.SetStartupReady(true)
				f.provider.check = func(context.Context) {}
				if preceding != "clear-draft-only" && preceding != "transcript" {
					settleRewindTurn(t, f, "first")
				}
				_, debounce := f.ui.Update(tea.PasteMsg{Content: "saved draft"})
				driveBeforeCommand(f, debounce)
				sessions, err := f.store.ListSessions(t.Context(), f.ws.Root())
				if err != nil || len(sessions) != 1 {
					t.Fatalf("saved draft: %v", err)
				}
				id := sessions[0].ID
				before, err := f.store.GetSession(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}

				var prior tea.Cmd
				if preceding == "transcript" {
					f.ui.AddTranscriptUser("first legacy entry")
				}
				if preceding == "full" || preceding == "transcript" {
					prior = f.ui.ForceTranscriptPersist()
				} else {
					for range len("saved draft") {
						_, debounce = f.ui.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
					}
					if f.ui.ComposerText() != "" || debounce == nil {
						t.Fatal("composer clear did not schedule persistence")
					}
					_, prior = f.ui.Update(debounce())
				}
				if prior == nil {
					t.Fatal("missing preceding persistence command")
				}
				// Capture the replacement's observation before the preceding write
				// executes. Commands are deliberately driven in FIFO order.
				_, debounce = f.ui.Update(tea.PasteMsg{Content: "replacement"})
				driveBeforeCommand(f, debounce)
				if preceding == "transcript" {
					f.ui.AddTranscriptUser("second legacy entry")
					f.ui.ForceTranscriptPersist()
				}
				_, debounce = f.ui.Update(tea.PasteMsg{Content: " latest"})
				driveBeforeCommand(f, debounce)
				if preceding == "transcript" {
					// Do not coalesce this over a write observed by the latest draft.
					f.ui.AddTranscriptUser("third legacy entry")
					f.ui.ForceTranscriptPersist()
				}
				want := f.ui.ComposerText()
				driveBeforeCommand(f, prior)
				after, err := f.store.GetSession(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				text, err := draft.Decode(after.PendingJSON)
				if err != nil || text != want {
					t.Fatalf("replacement draft = %q, want %q: %v", text, want, err)
				}
				if !reflect.DeepEqual(before.ContextJSON, after.ContextJSON) {
					t.Fatal("draft replacement changed conversation context")
				}
				if err := f.ui.SaveSession(t.Context()); err != nil {
					t.Fatalf("local FIFO write blocked subsequent save: %v", err)
				}
				if preceding != "transcript" {
					settleRewindTurn(t, f, "next ordinary turn")
				}
			})
		})
	}
}

func TestQueuedClearReceiptPreservesBeforeDuringShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first")
		_, debounce := f.ui.Update(tea.PasteMsg{Content: "saved"})
		driveBeforeCommand(f, debounce)
		for range len("saved") {
			_, debounce = f.ui.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
		}
		_, clearDraft := f.ui.Update(debounce())
		f.provider.check = func(context.Context) { t.Error("shutdown dispatched queued turn") }
		f.ui.Submit("protected replacement")
		if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
			t.Fatalf("queued clear blocked BEFORE recovery save: %v", err)
		}
		if msg := clearDraft(); msg != nil {
			t.Fatal("late clear ran after shutdown claimed it")
		}
		id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		saved, err := f.store.GetSession(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		text, err := draft.Decode(saved.PendingJSON)
		if err != nil || text != "protected replacement" {
			t.Fatalf("shutdown recovery draft = %q: %v", text, err)
		}
	})
}

func TestQueuedLocalWriteReceiptsRejectCompetingDraft(t *testing.T) {
	for _, preceding := range []string{"clear", "full"} {
		for _, timing := range []string{"before-local-commit", "after-local-commit"} {
			t.Run(preceding+"/"+timing, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f := newBeforeFixture(t)
					f.ui.SetStartupReady(true)
					f.provider.check = func(context.Context) {}
					settleRewindTurn(t, f, "first")
					_, debounce := f.ui.Update(tea.PasteMsg{Content: "saved"})
					driveBeforeCommand(f, debounce)
					id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
					if err != nil {
						t.Fatal(err)
					}
					var prior tea.Cmd
					if preceding == "full" {
						prior = f.ui.ForceTranscriptPersist()
					} else {
						for range len("saved") {
							_, debounce = f.ui.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
						}
						_, prior = f.ui.Update(debounce())
					}
					_, debounce = f.ui.Update(tea.PasteMsg{Content: "local replacement"})
					driveBeforeCommand(f, debounce)
					var ack tea.Msg
					if timing == "after-local-commit" {
						ack = prior() // receipt exists, but replacement is still queued
					}
					competing, err := f.store.GetSession(t.Context(), id)
					if err != nil {
						t.Fatal(err)
					}
					competing.PendingJSON, err = draft.Encode("competing writer")
					if err != nil {
						t.Fatal(err)
					}
					if err := f.store.SaveSessionDraft(t.Context(), competing); err != nil {
						t.Fatal(err)
					}
					before, err := f.store.GetSessionResumeState(t.Context(), id)
					if err != nil {
						t.Fatal(err)
					}
					if timing == "before-local-commit" {
						ack = prior()
					}
					_, next := f.ui.Update(ack)
					driveBeforeCommand(f, next)
					if err := f.ui.SaveSession(t.Context()); err == nil {
						t.Fatal("competing writer did not block a stale save")
					}
					after, err := f.store.GetSessionResumeState(t.Context(), id)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("local receipt adopted or overwrote competing content: %v", err)
					}
					if f.ui.ComposerText() == "" {
						t.Fatal("conflict discarded local composer")
					}
				})
			})
		}
	}
}
