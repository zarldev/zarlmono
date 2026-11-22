package db_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func accountingEnvelope(batch string) db.SessionProviderAccounting {
	return db.SessionProviderAccounting{RequestBatchID: batch, Version: 1, PayloadJSON: []byte(`{"opaque":"settled"}`)}
}

func saveAccountingReceipt(t *testing.T, store *db.Store, sessionID, batch string) {
	t.Helper()
	if err := store.SaveSessionHistory(t.Context(), sessionID, []db.SessionHistoryBatch{{ID: batch, RequestJSON: []byte(`{"request":true}`)}}); err != nil {
		t.Fatal(err)
	}
}

func initializeAccountingSession(t *testing.T, store *db.Store, sessionID, workspace string) {
	t.Helper()
	if err := store.SaveActiveSession(t.Context(), db.SessionRecord{ID: sessionID, Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(t.Context(), sessionID, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	saveCheckpoint(t, store, db.SessionCheckpoint{SessionID: sessionID, SourceSessionID: sessionID,
		ID: "initial", Workspace: workspace, BoundaryID: "initial", FormatVersion: 1, Payload: []byte(`{}`), History: ref})
}

func TestProviderAccountingOwnershipRetryAndReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "accounting.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initializeAccountingSession(t, store, "source", "/workspace")
	saveAccountingReceipt(t, store, "source", "request")
	entry := accountingEnvelope("request")
	want := entry.Clone()
	clone := entry.Clone()
	clone.PayloadJSON[0] = '!'
	if !bytes.Equal(entry.PayloadJSON, want.PayloadJSON) {
		t.Fatal("Clone aliases original")
	}
	if err := store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{entry}); err != nil {
		t.Fatal(err)
	}
	entry.PayloadJSON[0] = '!'
	got, err := store.GetSessionProviderAccounting(t.Context(), "source", "request")
	if err != nil || !reflect.DeepEqual(got.Accounting, want) || got.Sequence == 0 {
		t.Fatalf("stored envelope: %+v, %v", got, err)
	}
	sequence := got.Sequence
	got.Accounting.PayloadJSON[0] = '!'
	page, err := store.ListSessionProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0].Accounting, want) {
		t.Fatalf("page aliases Get: %v", err)
	}
	page[0].Accounting.PayloadJSON[0] = '!'
	if err := store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{want, want}); err != nil {
		t.Fatal(err)
	}
	changed := want.Clone()
	changed.PayloadJSON = []byte(`{"opaque":"different"}`)
	if err := store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{changed}); !errors.Is(err, db.ErrProviderAccountingConflict) {
		t.Fatalf("changed content: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err = reopened.GetSessionProviderAccounting(t.Context(), "source", "request")
	if err != nil || got.Sequence != sequence || !reflect.DeepEqual(got.Accounting, want) {
		t.Fatalf("reopen changed bytes/cursor: %+v, %v", got, err)
	}
	if _, err := reopened.GetSessionProviderAccounting(t.Context(), "other", "request"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cross-session lookup: %v", err)
	}
}

func TestProviderAccountingInputAndPaginationBounds(t *testing.T) {
	store, _ := historyStore(t)
	saveAccountingReceipt(t, store, "source", "request")
	for _, tc := range []struct {
		name    string
		session string
		entry   db.SessionProviderAccounting
		want    error
	}{
		{"empty scope", "", accountingEnvelope("request"), db.ErrProviderAccountingInvalidRequest},
		{"empty receipt", "source", accountingEnvelope(""), db.ErrProviderAccountingInvalidRequest},
		{"missing receipt", "source", accountingEnvelope("absent"), db.ErrProviderAccountingInvalidRequest},
		{"wrong scope", "other", accountingEnvelope("request"), db.ErrProviderAccountingInvalidRequest},
		{"zero version", "source", db.SessionProviderAccounting{RequestBatchID: "request", PayloadJSON: []byte(`{}`)}, db.ErrProviderAccountingVersion},
		{"unsupported version", "source", db.SessionProviderAccounting{RequestBatchID: "request", Version: math.MaxUint64, PayloadJSON: []byte(`{}`)}, db.ErrProviderAccountingVersion},
		{"empty JSON", "source", db.SessionProviderAccounting{RequestBatchID: "request", Version: 1}, db.ErrProviderAccountingInvalidRequest},
		{"invalid JSON", "source", db.SessionProviderAccounting{RequestBatchID: "request", Version: 1, PayloadJSON: []byte(`CANARY_PRIVATE_PAYLOAD`)}, db.ErrProviderAccountingInvalidRequest},
		{"trailing JSON", "source", db.SessionProviderAccounting{RequestBatchID: "request", Version: 1, PayloadJSON: []byte(`{} {}`)}, db.ErrProviderAccountingInvalidRequest},
		{"invalid UTF8", "source", db.SessionProviderAccounting{RequestBatchID: "request", Version: 1, PayloadJSON: []byte{'"', 0xff, '"'}}, db.ErrProviderAccountingInvalidRequest},
		{"over limit", "source", db.SessionProviderAccounting{RequestBatchID: "request", Version: 1, PayloadJSON: []byte(`"` + strings.Repeat("x", (1<<20)-1) + `"`)}, db.ErrProviderAccountingInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := store.SaveSessionProviderAccounting(t.Context(), tc.session, []db.SessionProviderAccounting{tc.entry})
			if !errors.Is(err, tc.want) || err.Error() != tc.want.Error() {
				t.Fatalf("input error = %v, want static %v", err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		session string
		cursor  uint64
		limit   int
	}{
		{"", 0, 1}, {"source", 0, 0}, {"source", 0, -1}, {"source", 0, 257}, {"source", math.MaxUint64, 1},
	} {
		if _, err := store.ListSessionProviderAccounting(t.Context(), tc.session, tc.cursor, tc.limit); !errors.Is(err, db.ErrProviderAccountingInvalidRequest) {
			t.Fatalf("pagination bounds: %v", err)
		}
	}
	if _, err := store.GetSessionProviderAccounting(t.Context(), "", "request"); !errors.Is(err, db.ErrProviderAccountingInvalidRequest) {
		t.Fatalf("Get scope: %v", err)
	}
	if _, err := store.GetSessionProviderAccounting(t.Context(), "source", ""); !errors.Is(err, db.ErrProviderAccountingInvalidRequest) {
		t.Fatalf("Get key: %v", err)
	}
	entry := accountingEnvelope("request")
	entry.PayloadJSON = []byte(`"` + strings.Repeat("x", (1<<20)-2) + `"`)
	if err := store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{entry}); err != nil {
		t.Fatalf("exact 1 MiB JSON (application schema is opaque): %v", err)
	}
	got, err := store.GetSessionProviderAccounting(t.Context(), "source", "request")
	if err != nil || !bytes.Equal(got.Accounting.PayloadJSON, entry.PayloadJSON) {
		t.Fatalf("1 MiB round trip: %v", err)
	}
	page, err := store.ListSessionProviderAccounting(t.Context(), "source", math.MaxInt64, 1)
	if err != nil || len(page) != 0 {
		t.Fatalf("max valid cursor: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.SaveSessionProviderAccounting(ctx, "source", []db.SessionProviderAccounting{entry}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
}

func TestProviderAccountingIndependentStoreConcurrency(t *testing.T) {
	first, _ := historyStore(t)
	var path, name string
	var sequence int
	if err := first.DB().QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	second, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	saveAccountingReceipt(t, first, "source", "same")
	saveAccountingReceipt(t, first, "source", "different")
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprintf("different=%t", different), func(t *testing.T) {
			batch := "same"
			if different {
				batch = "different"
			}
			start := make(chan struct{})
			errs := make(chan error, 12)
			var wg sync.WaitGroup
			for i := range 12 {
				store := first
				if i%2 != 0 {
					store = second
				}
				entry := accountingEnvelope(batch)
				if different {
					entry.PayloadJSON = fmt.Appendf(nil, `{"writer":%d}`, i)
				}
				wg.Go(func() {
					<-start
					errs <- store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{entry})
				})
			}
			close(start)
			wg.Wait()
			close(errs)
			successes := 0
			for err := range errs {
				if err == nil {
					successes++
				} else if !different || !errors.Is(err, db.ErrProviderAccountingConflict) {
					t.Fatalf("concurrent save: %v", err)
				}
			}
			want := 12
			if different {
				want = 1
			}
			if successes != want {
				t.Fatalf("successful writes = %d, want %d", successes, want)
			}
		})
	}
	page, err := first.ListSessionProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 2 {
		t.Fatalf("concurrent saves duplicated receipts: %v", err)
	}
}

func TestProviderAccountingAtomicSaveAndTransactionBoundStore(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	for _, batch := range []string{"existing", "first", "bound"} {
		saveAccountingReceipt(t, store, "source", batch)
	}
	if err := store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{accountingEnvelope("existing")}); err != nil {
		t.Fatal(err)
	}
	changed := accountingEnvelope("existing")
	changed.PayloadJSON = []byte(`{"changed":true}`)
	if err := store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{accountingEnvelope("first"), changed}); !errors.Is(err, db.ErrProviderAccountingConflict) {
		t.Fatalf("atomic conflict: %v", err)
	}
	if _, err := store.GetSessionProviderAccounting(t.Context(), "source", "first"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial batch escaped: %v", err)
	}
	rollback := errors.New("caller rollback")
	if err := store.WithTx(t.Context(), func(tx *db.Store) error {
		if err := tx.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{accountingEnvelope("bound")}); err != nil {
			return err
		}
		if _, err := tx.GetSessionProviderAccounting(t.Context(), "source", "bound"); err != nil {
			return err
		}
		if rows, err := tx.ListSessionProviderAccounting(t.Context(), "source", 0, 256); err != nil || len(rows) != 2 {
			t.Fatalf("transaction-bound read: %v", err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("transaction-bound save: %v", err)
	}
	if _, err := store.GetSessionProviderAccounting(t.Context(), "source", "bound"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("caller rollback escaped: %v", err)
	}
	if err := store.WithTx(t.Context(), func(tx *db.Store) error {
		return tx.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{accountingEnvelope("bound")})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderAccountingKeysetGapsForeignKeyAndCascade(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	initializeAccountingSession(t, store, "other", "/other")
	var sequences []uint64
	for _, session := range []string{"source", "other", "source", "source"} {
		batch := fmt.Sprintf("request-%d", len(sequences))
		saveAccountingReceipt(t, store, session, batch)
		if err := store.SaveSessionProviderAccounting(t.Context(), session, []db.SessionProviderAccounting{accountingEnvelope(batch)}); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetSessionProviderAccounting(t.Context(), session, batch)
		if err != nil {
			t.Fatal(err)
		}
		sequences = append(sequences, got.Sequence)
	}
	if _, err := store.DB().ExecContext(t.Context(), "DELETE FROM session_history_batches WHERE session_id = ? AND batch_id = ?", "source", "request-2"); err != nil {
		t.Fatal(err)
	}
	first, err := store.ListSessionProviderAccounting(t.Context(), "source", 0, 1)
	if err != nil || len(first) != 1 || first[0].Sequence != sequences[0] {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := store.ListSessionProviderAccounting(t.Context(), "source", first[0].Sequence, 1)
	if err != nil || len(second) != 1 || second[0].Sequence != sequences[3] {
		t.Fatalf("page skips opaque gaps: %+v, %v", second, err)
	}
	last, err := store.ListSessionProviderAccounting(t.Context(), "source", second[0].Sequence, 1)
	if err != nil || len(last) != 0 {
		t.Fatalf("end page: %v", err)
	}
	if _, err := store.GetSessionProviderAccounting(t.Context(), "source", "request-2"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("receipt deletion did not cascade: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO session_provider_accounting
		(session_id, request_batch_id, version, payload_json, checksum) VALUES ('source', 'request-1', 1, X'7b7d', 'invalid')`); err == nil {
		t.Fatal("FK accepted another session's receipt")
	}
	if err := store.DeleteSession(t.Context(), "source"); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListSessionProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 0 {
		t.Fatalf("session deletion did not cascade: %v", err)
	}
	page, err = store.ListSessionProviderAccounting(t.Context(), "other", 0, 256)
	if err != nil || len(page) != 1 {
		t.Fatalf("session deletion affected other scope: %v", err)
	}
}

func TestProviderAccountingStoredCorruptionIsSafe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		column string
		value  any
		want   error
	}{
		{"checksum", "checksum", "CANARY_PRIVATE_PAYLOAD", db.ErrProviderAccountingCorrupt},
		{"changed valid JSON", "payload_json", []byte(`{"canary":"CANARY_PRIVATE_PAYLOAD"}`), db.ErrProviderAccountingCorrupt},
		{"malformed JSON", "payload_json", []byte(`CANARY_PRIVATE_PAYLOAD`), db.ErrProviderAccountingCorrupt},
		{"invalid UTF8", "payload_json", []byte{'"', 0xff, '"'}, db.ErrProviderAccountingCorrupt},
		{"oversized JSON", "payload_json", []byte(`"` + strings.Repeat("x", 1<<20) + `"`), db.ErrProviderAccountingCorrupt},
		{"payload wrong type", "payload_json", "CANARY_PRIVATE_PAYLOAD", db.ErrProviderAccountingCorrupt},
		{"version wrong type", "version", "CANARY_PRIVATE_PAYLOAD", db.ErrProviderAccountingCorrupt},
		{"version negative", "version", -1, db.ErrProviderAccountingCorrupt},
		{"version zero", "version", 0, db.ErrProviderAccountingCorrupt},
		{"version unsupported", "version", 2, db.ErrProviderAccountingVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, _ := historyStore(t)
			saveAccountingReceipt(t, store, "source", "request")
			entry := accountingEnvelope("request")
			if err := store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{entry}); err != nil {
				t.Fatal(err)
			}
			// Column identifiers come only from this fixed test table.
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_provider_accounting SET "+tc.column+" = ?", tc.value); err != nil {
				t.Fatal(err)
			}
			_, getErr := store.GetSessionProviderAccounting(t.Context(), "source", "request")
			page, listErr := store.ListSessionProviderAccounting(t.Context(), "source", 0, 256)
			saveErr := store.SaveSessionProviderAccounting(t.Context(), "source", []db.SessionProviderAccounting{entry})
			for _, err := range []error{getErr, listErr, saveErr} {
				if !errors.Is(err, tc.want) || err.Error() != tc.want.Error() {
					t.Fatalf("stored error is not static identity %v: %v", tc.want, err)
				}
			}
			if page != nil {
				t.Fatal("corrupt page returned partial data")
			}
		})
	}
}
