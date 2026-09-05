package db_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestUpdateActiveTranscriptRejectsExistingForeignWorkspace(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	first := db.TranscriptUpdate{
		SessionID: "workspace-owned", Workspace: "/one", Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "one", Kind: "user_message", PayloadJSON: []byte(`{"text":"one"}`), Revision: 1}},
	}
	if err := store.UpdateActiveTranscript(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	foreign := first
	foreign.Workspace = "/two"
	foreign.ExpectedRevision = 1
	foreign.Revision = 2
	foreign.Entries = []db.TranscriptEntry{{Sequence: 2, EntryID: "two", Kind: "user_message", PayloadJSON: []byte(`{"text":"two"}`), Revision: 2}}
	if err := store.UpdateActiveTranscript(t.Context(), foreign); err == nil {
		t.Fatal("foreign workspace transcript update succeeded")
	}
	if _, err := store.GetSetting(t.Context(), "/two", "active_session"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("foreign workspace active session = %v, want not found", err)
	}
	stored, err := store.GetSessionTranscript(t.Context(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 1 || len(stored.Entries) != 1 || stored.Entries[0].EntryID != "one" {
		t.Fatalf("foreign update changed transcript: %#v", stored)
	}
}
