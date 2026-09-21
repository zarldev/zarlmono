package db_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSessionContentVersionLocalWriteReceipts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"clear", "transcript", "full", "checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "state.db")
			store, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			other, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			record := db.SessionRecord{ID: "session", Workspace: "/workspace",
				ContextJSON: []byte(`[{"role":"user","content":"retained context"}]`), PendingJSON: []byte(`{"text":"saved"}`)}
			update := db.TranscriptUpdate{SessionID: record.ID, Workspace: record.Workspace, Revision: 1,
				Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"first"}`), Revision: 1}}}
			if err := store.CommitCompletedTurn(t.Context(), record, update); err != nil {
				t.Fatal(err)
			}
			initial, err := store.SessionVersion(t.Context(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			update.ExpectedRevision, update.Revision = 1, 2
			update.Label = "local metadata"
			update.Entries = []db.TranscriptEntry{{Sequence: 2, EntryID: "e2", ParentID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"second"}`), Revision: 2}}
			record.PendingJSON = []byte(`{"text":"local"}`)
			write := func(expected db.SessionContentVersion) (db.SessionContentVersion, error) {
				switch kind {
				case "clear":
					return store.ClearSessionDraftVersioned(t.Context(), record.ID, expected)
				case "transcript":
					return store.UpdateActiveTranscriptVersioned(t.Context(), update, expected)
				case "full":
					return store.CommitCompletedTurnVersioned(t.Context(), record, update, expected)
				default:
					return store.CommitCheckpointTurnVersioned(t.Context(), record, update, expected)
				}
			}
			committed, err := write(initial)
			if err != nil || committed == initial {
				t.Fatalf("local write did not return changed version: %v", err)
			}
			observed, err := other.SessionVersion(t.Context(), record.ID)
			if err != nil || observed != committed {
				t.Fatalf("receipt differs from committed row: %v", err)
			}
			record.PendingJSON = []byte(`{"text":"competing"}`)
			if err := other.SaveSessionDraft(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			before, err := other.GetSessionResumeState(t.Context(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			version, err := write(committed)
			if !errors.Is(err, db.ErrCheckpointConflict) || version != (db.SessionContentVersion{}) {
				t.Fatalf("stale write published receipt or lost conflict identity: %v", err)
			}
			after, err := other.GetSessionResumeState(t.Context(), record.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("stale write changed competing row: %v", err)
			}
		})
	}
}

func TestClearSessionDraftVersionedRollsBackDeletionFailure(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := db.SessionRecord{ID: "draft", Workspace: "/workspace", PendingJSON: []byte(`{"text":"retained"}`)}
	initial, err := store.SaveSessionDraftVersioned(t.Context(), record, db.SessionContentVersion{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	version, err := store.ClearSessionDraftVersioned(t.Context(), record.ID, initial)
	if err == nil || version != (db.SessionContentVersion{}) {
		t.Fatal("failed clear published a receipt")
	}
	after, err := store.SessionVersion(t.Context(), record.ID)
	if err != nil || after != initial {
		t.Fatalf("delete failure did not roll back draft clear: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_delete"); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.ClearSessionDraftVersioned(t.Context(), record.ID, initial)
	if err != nil || deleted != (db.SessionContentVersion{}) {
		t.Fatalf("draft-only deletion receipt: %v", err)
	}
	if _, err := store.SaveSessionDraftVersioned(t.Context(), record, deleted); err != nil {
		t.Fatalf("replacement after deletion: %v", err)
	}
}
