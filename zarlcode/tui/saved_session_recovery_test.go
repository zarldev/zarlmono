package tui_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func brokenSavedFixture(t *testing.T) (*beforeFixture, string) {
	t.Helper()
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "first prompt")
	settleRewindTurn(t, f, "second prompt")
	id := f.ui.SessionIdentity()
	state, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	resume, err := rewind.DecodeResume(state.Session.ContextJSON)
	if err != nil {
		t.Fatal(err)
	}
	revision := state.Transcript.Revision
	sequence := uint64(len(state.Transcript.Entries))
	entries := []db.TranscriptEntry{
		{Sequence: sequence + 1, EntryID: "unsettled-answer", TurnID: "historical-child", Kind: "assistant_message", Revision: revision + 1, PayloadJSON: []byte(`{"text":"PRIVATE-history-canary"}`)},
		{Sequence: sequence + 2, EntryID: "unsettled-reasoning", TurnID: "historical-child", Kind: "reasoning", Revision: revision + 2, PayloadJSON: []byte(`{"text":"PRIVATE-reasoning-canary"}`)},
		{Sequence: sequence + 3, EntryID: "later-notice", Kind: "notice", Revision: revision + 3, PayloadJSON: []byte(`{"text":"later event"}`)},
	}
	record := state.Session
	record.ContextJSON, err = rewind.EncodeResume(revision+3, resume.Context, resume.Target, resume.SettledTurnID, revision+3, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	record.PendingJSON, err = draft.Encode("PRIVATE-original-draft")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CommitCompletedTurn(t.Context(), record, db.TranscriptUpdate{SessionID: id, Workspace: f.ws.Root(), ExpectedRevision: revision, Revision: revision + 3, Entries: entries}); err != nil {
		t.Fatal(err)
	}
	return f, id
}

func recoveryUI(t *testing.T, f *beforeFixture, withRuntime bool) *tui.UI {
	t.Helper()
	ui := tui.New()
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	ui.SetSettings(settings)
	if withRuntime {
		ui.SetLiveRunner(f.live)
	}
	ui.OpenIntro(f.ws.Root())
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 45})
	return ui
}

func TestSavedInspectionIsReadOnlyWithoutRuntime(t *testing.T) {
	f, id := brokenSavedFixture(t)
	f.provider.check = func(context.Context) { t.Fatal("inspection executed provider") }
	before, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	ui := recoveryUI(t, f, false)
	if err := ui.OpenSavedSessionInspection(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "2 unfinished entries") || strings.Contains(view, "PRIVATE-") {
		t.Fatal("missing safe diagnostic or private content leaked into diagnostic")
	}
	ui.Update(tea.KeyPressMsg{Code: 'v'})
	if !strings.Contains(ansi.Strip(ui.View().Content), "READ-ONLY") {
		t.Fatal("history is not identified as read-only")
	}
	ui.Update(tea.KeyPressMsg{Code: 'r'}) // live rewind is disabled for saved history
	if !strings.Contains(ansi.Strip(ui.View().Content), "READ-ONLY") {
		t.Fatal("saved reader invoked live rewind")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	ui.Update(tea.KeyPressMsg{Code: 'd'})
	if !strings.Contains(ansi.Strip(ui.View().Content), "PRIVATE-original-draft") {
		t.Fatal("original draft is not retrievable")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	ui.Update(tea.KeyPressMsg{Code: 'r'})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(ui.View().Content), "Files were not restored") {
		t.Fatal("missing recovery preview")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	after, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("inspection changed source", err)
	}
	if ui.SessionIdentity() != "" || !ui.CanonicalThread().IsEmpty() || ui.ComposerText() != "" {
		t.Fatal("inspection activated saved history")
	}
	active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || active != id {
		t.Fatal("inspection changed active selection", err)
	}
}

func TestSavedRecoveryRequiresReviewedConfirmationAndPreservesSource(t *testing.T) {
	for _, scenario := range []string{"recover", "source conflict", "selection conflict", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			f, id := brokenSavedFixture(t)
			f.provider.check = func(context.Context) { t.Fatal("recovery submitted a turn") }
			if err := f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", "other-session"); err != nil {
				t.Fatal(err)
			}
			ui := recoveryUI(t, f, true)
			if err := ui.OpenSavedSessionInspection(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			ui.Update(tea.KeyPressMsg{Code: 'r'})
			candidates, err := f.store.ListSessionCheckpoints(t.Context(), id)
			if err != nil || len(candidates) != 2 {
				t.Fatal("expected two candidates", err)
			}
			// Explicitly choose the second candidate, not an automatic latest-valid fallback.
			ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			chosen, err := f.store.GetSessionCheckpoint(t.Context(), id, candidates[1].ID)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, err := rewind.Load(t.Context(), f.store, chosen)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := checkpoint.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // not rendered/reviewed yet
			active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil || active != "other-session" {
				t.Fatal("created child before review", err)
			}
			switch scenario {
			case "source conflict":
				record, err := f.store.GetSession(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				record.PendingJSON = []byte(`"competing draft"`)
				if err := f.store.SaveSession(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			case "selection conflict":
				if err := f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", "competing-selection"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			beforeContext := f.live.ContextSnapshot()
			_ = ui.View()
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			_ = ui.View()
			if scenario == "cancel" {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			} else {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			after, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("recovery changed source", err)
			}
			if !reflect.DeepEqual(beforeContext, f.live.ContextSnapshot()) || ui.SessionIdentity() != "" {
				t.Fatal("recovery activated runtime")
			}
			active, err = f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "recover" {
				expected := "other-session"
				if scenario == "selection conflict" {
					expected = "competing-selection"
				}
				if active != expected {
					t.Fatal("cancel/conflict changed active selection")
				}
				return
			}
			if active == id || active == "other-session" {
				t.Fatalf("child not created: %s", ansi.Strip(ui.View().Content))
			}
			branch, err := f.store.GetSessionBranch(t.Context(), active)
			if err != nil || branch.SourceSessionID != id || branch.SourceCheckpointID != chosen.ID {
				t.Fatal("wrong recovery provenance", err)
			}
			child, err := f.store.GetSessionResumeState(t.Context(), active)
			if err != nil {
				t.Fatal(err)
			}
			savedDraft, err := draft.Decode(child.Session.PendingJSON)
			if err != nil || savedDraft != snapshot.Boundary.PromptText {
				t.Fatal("recovered prompt was not preserved as draft", err)
			}
			restarted := recoveryUI(t, f, true)
			if err := restarted.ResumeSavedSession(t.Context(), active); err != nil {
				t.Fatal("recovery child rejected by strict resume", err)
			}
			if err := restarted.ResumeSavedSession(t.Context(), id); !errors.Is(err, transcript.ErrCheckpointUnsettled) {
				t.Fatal("source was silently repaired", err)
			}
		})
	}
}

func TestLatestExactSourceNeverFallsBack(t *testing.T) {
	f, id := brokenSavedFixture(t)
	// An eligible alternative makes silent fallback observable, rather than
	// merely asserting the error returned when there is only one saved session.
	pending, err := draft.Encode("unrelated draft")
	if err != nil {
		t.Fatal(err)
	}
	const otherID = "unrelated-session"
	if err := f.store.SaveSession(t.Context(), db.SessionRecord{ID: otherID, Workspace: f.ws.Root(), ContextJSON: []byte(`[]`), PendingJSON: pending}); err != nil {
		t.Fatal(err)
	}
	other := recoveryUI(t, f, false)
	if err := other.ResumeSavedSession(t.Context(), otherID); err != nil {
		t.Fatal("fallback fixture is not resumable", err)
	}
	if err := f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", id); err != nil {
		t.Fatal(err)
	}
	ui := recoveryUI(t, f, true)
	if err := ui.ResumeLatestSavedSession(t.Context()); !errors.Is(err, transcript.ErrCheckpointUnsettled) {
		t.Fatalf("latest resume=%v", err)
	}
	active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || active != id || ui.SessionIdentity() != "" {
		t.Fatal("latest exact failure changed selection", err)
	}
}

func TestSavedInspectionWithoutCheckpointKeepsHistoryAvailable(t *testing.T) {
	f, id := brokenSavedFixture(t)
	if _, err := f.store.ExpireSessionCheckpoints(t.Context(), time.Now().Add(2*db.CheckpointRetention)); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	ui := recoveryUI(t, f, false)
	if err := ui.OpenSavedSessionInspection(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	ui.Update(tea.KeyPressMsg{Code: 'r'})
	if !strings.Contains(ansi.Strip(ui.View().Content), "No verified continuation available") {
		t.Fatal("missing honest no-checkpoint result: " + ansi.Strip(ui.View().Content))
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui.Update(tea.KeyPressMsg{Code: 'v'})
	if !strings.Contains(ansi.Strip(ui.View().Content), "READ-ONLY") {
		t.Fatal("history unavailable without checkpoint")
	}
	after, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("no-checkpoint inspection changed source", err)
	}
}
