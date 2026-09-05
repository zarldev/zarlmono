package tui_test

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestResumePersistsCrashOpenAssistantRecoveryOnce(t *testing.T) {
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

	const sessionID = "crash-open-assistant"
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: sessionID, Workspace: workspaceRoot, Revision: 2,
		Entries: []db.TranscriptEntry{
			{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"hello"}`), Revision: 1},
			{Sequence: 2, EntryID: "e2", TurnID: "turn", Kind: "assistant_message", PayloadJSON: []byte(`{}`), Revision: 2},
		},
	}); err != nil {
		t.Fatal(err)
	}

	resume := func() {
		ui := tui.New()
		ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
		ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
		if err := ui.ResumeSavedSession(t.Context(), sessionID); err != nil {
			t.Fatalf("resume: %v", err)
		}
	}
	resume()
	stored, err := store.GetSessionTranscript(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 3 {
		t.Fatalf("recovered revision = %d, want 3", stored.Revision)
	}
	resume()
	stored, err = store.GetSessionTranscript(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 3 {
		t.Fatalf("second resume revision = %d, want 3", stored.Revision)
	}
}
