package db_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func preparedHistoryBoundary(checkpoint db.SessionCheckpoint, entries []db.TranscriptEntry) db.HistoryCheckpoint {
	state := []byte(`{"boundary":"before"}`)
	digest := sha256.Sum256(state)
	checkpoint.History = db.CheckpointHistory{StateID: hex.EncodeToString(digest[:])}
	checkpoint.Payload = []byte(`{"state_id":"` + checkpoint.History.StateID + `"}`)
	return db.HistoryCheckpoint{Checkpoint: checkpoint, Entries: entries, State: state}
}

func TestBeforeHistoryPublicationAtomicRetry(t *testing.T) {
	for _, mode := range []string{"initial", "record", "delta"} {
		t.Run(mode, func(t *testing.T) {
			var store *db.Store
			var checkpoint db.SessionCheckpoint
			if mode == "initial" {
				var err error
				store, err = db.Open(t.Context(), t.TempDir()+"/initial.db")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				checkpoint = db.SessionCheckpoint{SessionID: "source", SourceSessionID: "source", Workspace: t.TempDir(), FormatVersion: 2, BoundaryID: "prompt"}
			} else {
				store, checkpoint = historyStore(t)
			}
			checkpoint.ID = "atomic-before"
			record := db.SessionRecord{ID: "source", Workspace: checkpoint.Workspace, ContextJSON: []byte(`[]`), PendingJSON: []byte(`{"text":"retry me"}`)}
			update := db.TranscriptUpdate{SessionID: record.ID, Workspace: record.Workspace}
			if mode == "delta" {
				update.Revision = 1
				update.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "answer", Revision: 1, Kind: "assistant_message", PayloadJSON: []byte(`{"text":"settled"}`)}}
				checkpoint.SourceRevision = 1
			}
			boundary := preparedHistoryBoundary(checkpoint, update.Entries)
			batch := db.SessionHistoryBatch{ID: "stable-before", Messages: [][]byte{[]byte(`{"message":"same"}`), []byte(`{"message":"same"}`)}, RequestJSON: []byte(`{"prepared":true}`)}
			version, err := store.SessionVersion(t.Context(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			before, beforeErr := store.GetSessionResumeState(t.Context(), record.ID)
			commit := func() error { return store.CommitBeforeTurn(t.Context(), record, update, boundary, "", version, batch) }
			if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_before BEFORE INSERT ON session_checkpoints BEGIN SELECT RAISE(ABORT, 'injected checkpoint failure'); END`); err != nil {
				t.Fatal(err)
			}
			if err := commit(); err == nil {
				t.Fatal("checkpoint failure ignored")
			}
			after, afterErr := store.GetSessionResumeState(t.Context(), record.ID)
			if !errors.Is(afterErr, beforeErr) || !reflect.DeepEqual(before, after) {
				t.Fatalf("partial session/transcript settlement: %v, %v", beforeErr, afterErr)
			}
			afterVersion, err := store.SessionVersion(t.Context(), record.ID)
			if err != nil || version != afterVersion {
				t.Fatalf("session row escaped rollback: %v", err)
			}
			if _, err := store.GetSessionCheckpoint(t.Context(), record.ID, checkpoint.ID); !errors.Is(err, db.ErrCheckpointUnavailable) {
				t.Fatalf("checkpoint escaped rollback: %v", err)
			}
			if _, err := store.GetSessionModelContext(t.Context(), record.ID); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("request escaped rollback: %v", err)
			}
			ref, err := store.CaptureSessionHistory(t.Context(), record.ID, nil, []byte(`{}`))
			if err != nil || ref.ReplayHead != "" {
				t.Fatalf("replay escaped rollback: %v", err)
			}
			if mode == "initial" {
				if _, err := store.GetSettingExact(t.Context(), record.Workspace, "active_session"); !errors.Is(err, db.ErrNotFound) {
					t.Fatalf("active pointer escaped rollback: %v", err)
				}
			}
			if _, err := store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_before`); err != nil {
				t.Fatal(err)
			}
			if err := commit(); err != nil {
				t.Fatal(err)
			}
			saved, err := store.GetSessionCheckpoint(t.Context(), record.ID, checkpoint.ID)
			if err != nil {
				t.Fatal(err)
			}
			entries, messages, state, err := store.ReadCheckpointHistory(t.Context(), saved.History)
			if err != nil || len(messages) != 2 || len(entries) != len(boundary.Entries) || string(state) != string(boundary.State) {
				t.Fatalf("incomplete checkpoint boundary: %v", err)
			}
			request, err := store.GetSessionModelContext(t.Context(), record.ID)
			if err != nil || request.Generation != 1 || request.HistoryHead != saved.History.ReplayHead {
				t.Fatalf("incorrect request boundary: %v", err)
			}
			if err := commit(); !errors.Is(err, db.ErrCheckpointConflict) {
				t.Fatalf("ambiguous publication permitted redispatch: %v", err)
			}
			if err := store.SaveSessionHistory(t.Context(), record.ID, []db.SessionHistoryBatch{batch}); err != nil {
				t.Fatal(err)
			}
			ref, err = store.CaptureSessionHistory(t.Context(), record.ID, nil, []byte(`{}`))
			if err != nil || ref.ReplayHead != saved.History.ReplayHead {
				t.Fatalf("retry duplicated occurrences: %v", err)
			}
		})
	}
}

func TestBeforeHistoryConflictDoesNotPublish(t *testing.T) {
	for _, conflict := range []string{"content", "prefix"} {
		t.Run(conflict, func(t *testing.T) {
			store, checkpoint := historyStore(t)
			record, err := store.GetSession(t.Context(), checkpoint.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			version, err := store.SessionVersion(t.Context(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint.ID = "conflicting-before"
			boundary := preparedHistoryBoundary(checkpoint, nil)
			update := db.TranscriptUpdate{SessionID: record.ID, Workspace: record.Workspace}
			want := db.ErrCheckpointConflict
			switch conflict {
			case "content":
				other := record
				other.PendingJSON = []byte(`{"text":"competing draft"}`)
				if err := store.SaveSession(t.Context(), other); err != nil {
					t.Fatal(err)
				}
			case "prefix":
				boundary.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "invented", Kind: "assistant_message", Revision: 1, PayloadJSON: []byte(`{}`)}}
				want = db.ErrTranscriptConflict
			}
			before, err := store.GetSessionResumeState(t.Context(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			batch := db.SessionHistoryBatch{ID: "pending", Messages: [][]byte{[]byte(`{"message":"retained"}`)}, RequestJSON: []byte(`{}`)}
			if err := store.CommitBeforeTurn(t.Context(), record, update, boundary, "", version, batch); !errors.Is(err, want) {
				t.Fatalf("conflict: %v", err)
			}
			after, err := store.GetSessionResumeState(t.Context(), record.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("conflict overwrote recovery: %v", err)
			}
			if _, err := store.GetSessionCheckpoint(t.Context(), record.ID, checkpoint.ID); !errors.Is(err, db.ErrCheckpointUnavailable) {
				t.Fatalf("published on conflict: %v", err)
			}
			ref, err := store.CaptureSessionHistory(t.Context(), record.ID, nil, []byte(`{}`))
			if err != nil || ref.ReplayHead != "" {
				t.Fatalf("history published on conflict: %v", err)
			}
		})
	}
}
