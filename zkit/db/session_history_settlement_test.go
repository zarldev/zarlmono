package db_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestHistorySettlementAtomicRetry(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		for _, advance := range []bool{false, true} {
			name := "unrestricted"
			if guarded {
				name = "guarded"
			}
			if advance {
				name += "/delta"
			} else {
				name += "/record"
			}
			t.Run(name, func(t *testing.T) {
				store, _ := historyStore(t)
				before, err := store.GetSessionResumeState(t.Context(), "source")
				if err != nil {
					t.Fatal(err)
				}
				version, err := store.SessionVersion(t.Context(), "source")
				if err != nil {
					t.Fatal(err)
				}
				record := before.Session
				record.ContextJSON = []byte(`[{"role":"assistant","content":"settled"}]`)
				update := db.TranscriptUpdate{SessionID: "source", Workspace: record.Workspace}
				if advance {
					update.Revision = 1
					update.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "answer", Revision: 1, Kind: "assistant_message", PayloadJSON: []byte(`{"text":"settled"}`)}}
				}
				batch := db.SessionHistoryBatch{ID: "stable", Messages: [][]byte{[]byte(`{"message":"same"}`), []byte(`{"message":"same"}`)}, RequestJSON: []byte(`{"prepared":true}`)}
				commit := func() error {
					if guarded {
						return store.CommitCheckpointTurn(t.Context(), record, update, version, batch)
					}
					return store.CommitCompletedTurn(t.Context(), record, update, batch)
				}
				if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_receipt BEFORE INSERT ON session_history_batches BEGIN SELECT RAISE(ABORT, 'injected write failure'); END`); err != nil {
					t.Fatal(err)
				}
				if err := commit(); err == nil {
					t.Fatal("injected failure was ignored")
				}
				after, err := store.GetSessionResumeState(t.Context(), "source")
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("partial settlement: %v", err)
				}
				boundary, err := store.CaptureSessionHistory(t.Context(), "source", nil, []byte(`{}`))
				if err != nil || boundary.ReplayHead != "" {
					t.Fatalf("history escaped rollback: %v", err)
				}
				if _, err := store.GetSessionModelContext(t.Context(), "source"); !errors.Is(err, db.ErrNotFound) {
					t.Fatalf("request escaped rollback: %v", err)
				}
				if _, err := store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_receipt`); err != nil {
					t.Fatal(err)
				}
				if err := commit(); err != nil {
					t.Fatal(err)
				}
				// Reconcile an ambiguous full save without appending the batch twice.
				version, err = store.SessionVersion(t.Context(), "source")
				if err != nil {
					t.Fatal(err)
				}
				update.ExpectedRevision, update.Entries = update.Revision, nil
				if err := commit(); err != nil {
					t.Fatal(err)
				}
				boundary, err = store.CaptureSessionHistory(t.Context(), "source", nil, []byte(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				_, messages, _, err := store.ReadCheckpointHistory(t.Context(), boundary)
				if err != nil || len(messages) != 2 {
					t.Fatalf("retry duplicated or lost occurrences: %d, %v", len(messages), err)
				}
				model, err := store.GetSessionModelContext(t.Context(), "source")
				if err != nil || model.Generation != 1 || model.HistoryHead != boundary.ReplayHead {
					t.Fatalf("request receipt/head: %+v, %v", model, err)
				}
				batch.Messages[0] = []byte(`{"changed":true}`)
				if err := store.SaveSessionHistory(t.Context(), "source", []db.SessionHistoryBatch{batch}); !errors.Is(err, db.ErrCheckpointConflict) {
					t.Fatalf("reused identity: %v", err)
				}
			})
		}
	}
}
