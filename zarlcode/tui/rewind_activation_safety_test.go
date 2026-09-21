package tui_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestRewindApplyFailuresPreserveSourceRuntime(t *testing.T) {
	for _, scenario := range []string{"active pointer", "source revision", "expired checkpoint", "corrupt checkpoint", "provider build", "child transaction"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBeforeFixture(t)
			settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
			registry := settings.Registry
			settings.Registry = nil
			f.ui.SetSettings(settings)
			f.provider.check = func(context.Context) {}
			settleRewindTurn(t, f, "first")
			sourceID, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil {
				t.Fatal(err)
			}
			before := f.live.ContextSnapshot()
			target := f.live.RunTarget()
			previewRewindPrompt(f, 1)
			if view := f.ui.View().Content; strings.Contains(view, "Unavailable:") {
				t.Fatal(view)
			}
			wantActive := sourceID
			switch scenario {
			case "active pointer":
				wantActive = "other"
				err = f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", wantActive)
			case "source revision":
				var stored db.SessionTranscript
				stored, err = f.store.GetSessionTranscript(t.Context(), sourceID)
				if err == nil {
					entry := stored.Entries[len(stored.Entries)-1]
					entry.Revision = stored.Revision + 1
					err = f.store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: sourceID, Workspace: f.ws.Root(), ExpectedRevision: stored.Revision, Revision: entry.Revision, Entries: []db.TranscriptEntry{entry}})
				}
			case "expired checkpoint":
				_, err = f.store.ExpireSessionCheckpoints(t.Context(), time.Now().Add(31*24*time.Hour))
			case "corrupt checkpoint":
				_, err = f.store.DB().ExecContext(t.Context(), "UPDATE session_checkpoints SET checksum = 'corrupt'")
			case "provider build":
				settings.Registry = registry // unavailable saved route: no registry definitions loaded
			case "child transaction":
				_, err = f.store.DB().ExecContext(t.Context(), "CREATE TRIGGER reject_child BEFORE INSERT ON session_branches BEGIN SELECT RAISE(ABORT, 'injected branch failure'); END")
			}
			if err != nil {
				t.Fatal(err)
			}
			f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil || active != wantActive {
				t.Fatalf("active changed: %q %v", active, err)
			}
			if !reflect.DeepEqual(f.live.ContextSnapshot(), before) || !reflect.DeepEqual(f.live.RunTarget(), target) {
				t.Fatal("rejected activation mutated runtime")
			}
			sessions, err := f.store.ListSessions(t.Context(), f.ws.Root())
			if err != nil || len(sessions) != 1 {
				t.Fatalf("partial child persisted: %d %v", len(sessions), err)
			}
			if f.ui.ToastText() == "" {
				t.Fatal("activation failure not surfaced")
			}
			r, err := f.live.ReserveRuntime()
			if err != nil {
				t.Fatal("activation leaked reservation", err)
			}
			r.Release()
		})
	}
}

func TestRewindInitialBoundaryAndCancel(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "first")
	before := f.live.ContextSnapshot()
	previewRewindPrompt(f, 1)
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !reflect.DeepEqual(before, f.live.ContextSnapshot()) {
		t.Fatal("cancel mutated context")
	}
	// Closing the preview leaves the reader open at the same selected prompt.
	f.ui.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	f.ui.View()
	_, cmd := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	driveBeforeCommand(f, cmd)
	context := f.live.ContextSnapshot()
	if len(context) != 1 || context[0].Content != rewind.FilesUnchangedNotice || f.ui.ComposerText() != "first" {
		t.Fatal("initial boundary was not restored without submission")
	}
	id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		if err := f.ui.SaveSession(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range stored.Transcript.Entries {
		if strings.Contains(string(entry.PayloadJSON), rewind.FilesUnchangedNotice) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("notice count = %d", count)
	}
}
