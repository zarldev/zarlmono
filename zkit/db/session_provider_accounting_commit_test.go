package db_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestProviderAccountingAtomicTurnCommits(t *testing.T) {
	t.Parallel()
	for _, guarded := range []bool{false, true} {
		for _, advance := range []bool{false, true} {
			t.Run(fmt.Sprintf("guarded=%t/advance=%t", guarded, advance), func(t *testing.T) {
				t.Parallel()
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
				record.LastUsageJSON = []byte(`{"CostUSD":0.75}`)
				update := db.TranscriptUpdate{SessionID: "source", Workspace: record.Workspace}
				if advance {
					update.Revision = 1
					update.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "answer", Revision: 1, Kind: "assistant_message", PayloadJSON: []byte(`{"text":"settled"}`)}}
				}
				batch := db.SessionHistoryBatch{ID: "settled", Messages: [][]byte{[]byte(`{"message":"same"}`), []byte(`{"message":"same"}`)}, RequestJSON: []byte(`{"prepared":true}`)}
				entry := accountingEnvelope(batch.ID)
				commit := func(accounting []db.SessionProviderAccounting, history ...db.SessionHistoryBatch) error {
					if guarded {
						return store.CommitCheckpointTurnWithProviderAccounting(t.Context(), record, update, version, accounting, history...)
					}
					return store.CommitCompletedTurnWithProviderAccounting(t.Context(), record, update, accounting, history...)
				}
				// Failure at the last write must roll back all preceding components.
				if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_accounting BEFORE INSERT ON session_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected accounting write failure'); END`); err != nil {
					t.Fatal(err)
				}
				if err := commit([]db.SessionProviderAccounting{entry}, batch); err == nil {
					t.Fatal("injected accounting failure was ignored")
				}
				after, err := store.GetSessionResumeState(t.Context(), "source")
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("failed accounting partially committed session/transcript: %v", err)
				}
				if _, err := store.GetSessionModelContext(t.Context(), "source"); !errors.Is(err, db.ErrNotFound) {
					t.Fatalf("prepared request escaped rollback: %v", err)
				}
				assertAccountingReceiptCount(t, store, 0)
				if _, err := store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_accounting`); err != nil {
					t.Fatal(err)
				}
				// Accounting without its request receipt must roll back the full save.
				if err := commit([]db.SessionProviderAccounting{entry}); !errors.Is(err, db.ErrProviderAccountingInvalidRequest) {
					t.Fatalf("unpaired settlement: %v", err)
				}
				after, err = store.GetSessionResumeState(t.Context(), "source")
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("missing receipt partially committed: %v", err)
				}
				if err := commit([]db.SessionProviderAccounting{entry}, batch); err != nil {
					t.Fatal(err)
				}
				got, err := store.GetSessionProviderAccounting(t.Context(), "source", batch.ID)
				if err != nil || !reflect.DeepEqual(got.Accounting, entry) {
					t.Fatalf("settlement did not commit with receipt: %v", err)
				}
				after, err = store.GetSessionResumeState(t.Context(), "source")
				if err != nil || string(after.Session.ContextJSON) != string(record.ContextJSON) || string(after.Session.LastUsageJSON) != string(record.LastUsageJSON) || after.Transcript.Revision != update.Revision || after.Transcript.FormatVersion != db.SessionTranscriptFormatVersion {
					t.Fatalf("existing persistence contract changed: %v", err)
				}
				if !advance && !reflect.DeepEqual(before.Transcript, after.Transcript) {
					t.Fatal("same-revision settlement rewrote canonical transcript")
				}
				// Retry after a possibly ambiguous full-save commit, using the now
				// durable canonical revision, must not duplicate history or accounting.
				version, err = store.SessionVersion(t.Context(), "source")
				if err != nil {
					t.Fatal(err)
				}
				update.ExpectedRevision, update.Entries = update.Revision, nil
				if err := commit([]db.SessionProviderAccounting{entry}, batch); err != nil {
					t.Fatal(err)
				}
				again, err := store.GetSessionProviderAccounting(t.Context(), "source", batch.ID)
				if err != nil || !reflect.DeepEqual(got, again) {
					t.Fatalf("retry changed settlement cursor/content: %v", err)
				}
				assertAccountingReceiptCount(t, store, 1)
				model, err := store.GetSessionModelContext(t.Context(), "source")
				if err != nil || model.Generation != 1 {
					t.Fatalf("retry duplicated prepared request: %v", err)
				}
				boundary, err := store.CaptureSessionHistory(t.Context(), "source", nil, []byte(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				_, messages, _, err := store.ReadCheckpointHistory(t.Context(), boundary)
				if err != nil || len(messages) != 2 {
					t.Fatalf("retry duplicated history: %v", err)
				}
				// A changed settlement conflicts after the existing save operation;
				// neither its changed context nor a new receipt may leak out.
				version, err = store.SessionVersion(t.Context(), "source")
				if err != nil {
					t.Fatal(err)
				}
				before, err = store.GetSessionResumeState(t.Context(), "source")
				if err != nil {
					t.Fatal(err)
				}
				record.ContextJSON = []byte(`[{"different":"context"}]`)
				entry.PayloadJSON = []byte(`{"different":"settlement"}`)
				if err := commit([]db.SessionProviderAccounting{entry}, db.SessionHistoryBatch{ID: "must-rollback"}); !errors.Is(err, db.ErrProviderAccountingConflict) {
					t.Fatalf("changed settlement full commit: %v", err)
				}
				after, err = store.GetSessionResumeState(t.Context(), "source")
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("conflicting settlement partially committed: %v", err)
				}
				assertAccountingReceiptCount(t, store, 1)
			})
		}
	}
}

func assertAccountingReceiptCount(t *testing.T, store *db.Store, want int) {
	t.Helper()
	for _, table := range []string{"session_history_batches", "session_provider_accounting"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s rows = %d, want %d: %v", table, count, want, err)
		}
	}
}

func TestProviderAccountingCheckpointSourceConflict(t *testing.T) {
	t.Parallel()
	for _, advance := range []bool{false, true} {
		t.Run(fmt.Sprintf("advance=%t", advance), func(t *testing.T) {
			t.Parallel()
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
			record.PendingJSON = []byte(`[{"foreign":"draft"}]`)
			if err := store.SaveSession(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			before, err = store.GetSessionResumeState(t.Context(), "source")
			if err != nil {
				t.Fatal(err)
			}
			update := db.TranscriptUpdate{SessionID: "source", Workspace: record.Workspace}
			if advance {
				update.Revision = 1
				update.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "answer", Revision: 1, Kind: "assistant_message", PayloadJSON: []byte(`{"text":"settled"}`)}}
			}
			err = store.CommitCheckpointTurnWithProviderAccounting(t.Context(), record, update, version,
				[]db.SessionProviderAccounting{accountingEnvelope("request")}, db.SessionHistoryBatch{ID: "request", RequestJSON: []byte(`{}`)})
			if !errors.Is(err, db.ErrCheckpointConflict) {
				t.Fatalf("stale source: %v", err)
			}
			after, err := store.GetSessionResumeState(t.Context(), "source")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("source conflict partially committed: %v", err)
			}
			assertAccountingReceiptCount(t, store, 0)
		})
	}
}
