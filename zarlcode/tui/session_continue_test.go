package tui_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestContinueResumesWorkspaceActiveSession(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for i, id := range []string{"active", "newer"} {
		if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
			SessionID: id, Workspace: workspaceRoot, CreatedAt: time.Now().Add(time.Duration(i) * time.Hour), Revision: 1,
			Entries: []db.TranscriptEntry{{
				Sequence: 1, EntryID: id + "-e1", Kind: "user_message",
				PayloadJSON: []byte(`{"text":"` + id + `"}`), Revision: 1,
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	settings := engine.NewSettings(store, nil, nil, workspaceRoot)
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, "active_session", "active"); err != nil {
		t.Fatal(err)
	}

	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(settings)
	if err := ui.ResumeLatestSavedSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries := ui.CanonicalThread().Entries()
	if len(entries) != 1 || entries[0].Payload.Text != "active" {
		t.Fatalf("continued transcript = %#v", entries)
	}
}
