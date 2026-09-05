package tui_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestResumeRejectsSessionFromAnotherWorkspace(t *testing.T) {
	workspaceRoot := t.TempDir()
	otherRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "other", Workspace: otherRoot, Revision: 1,
		Entries: []db.TranscriptEntry{{
			Sequence: 1, EntryID: "e1", Kind: "user_message",
			PayloadJSON: []byte(`{"text":"other workspace"}`), Revision: 1,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	err = ui.ResumeSavedSession(t.Context(), "other")
	if err == nil || !strings.Contains(err.Error(), "belongs to workspace") {
		t.Fatalf("resume error = %v", err)
	}
	if !ui.CanonicalThread().IsEmpty() {
		t.Fatal("cross-workspace resume mutated the active transcript")
	}
}
