package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSessionTranscriptExposesFutureFormatVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "future", Workspace: "/workspace", Revision: 1,
		Entries: []db.TranscriptEntry{{
			Sequence: 1, EntryID: "e1", Kind: "user_message",
			PayloadJSON: []byte(`{"text":"hello"}`), Revision: 1,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(t.Context(), `UPDATE session_transcripts SET format_version = 99 WHERE session_id = 'future'`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.GetSessionTranscript(t.Context(), "future")
	if err != nil {
		t.Fatal(err)
	}
	if got.FormatVersion != 99 {
		t.Fatalf("format version = %d, want 99", got.FormatVersion)
	}
}
