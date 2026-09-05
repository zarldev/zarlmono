package tui_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestTranscriptConflictRebasesAgainstDurableRevision(t *testing.T) {
	workspaceRoot := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("rebase", "Rebase", false, time.Now())
	ui.AddPartialTranscript("turn", "prompt", "response")
	records, err := ui.CanonicalThread().RecordsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	first := records[0]
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "rebase", Workspace: workspaceRoot,
		Revision: first.Revision,
		Entries: []db.TranscriptEntry{{
			Sequence: first.Sequence, EntryID: first.ID, ParentID: first.ParentID,
			TurnID: first.TurnID, Kind: first.Kind, PayloadJSON: first.Payload, Revision: first.Revision,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	cmd := ui.ForceTranscriptPersist()
	if cmd == nil {
		t.Fatal("transcript persistence returned no command")
	}
	ui.Update(cmd())

	stored, err := store.GetSessionTranscript(t.Context(), "rebase")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Entries) != len(records) || stored.Revision != ui.CanonicalThread().Revision() {
		t.Fatalf("rebased transcript = revision %d, entries %d", stored.Revision, len(stored.Entries))
	}
}
