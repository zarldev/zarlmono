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

func childAccountingEnvelope(batch string) db.SessionChildProviderAccounting {
	return db.SessionChildProviderAccounting{OwnerRequestBatchID: batch, ChildTaskID: batch, Attempt: 1, Version: 1, PayloadJSON: []byte(`{"opaque":"settled"}`)}
}

func TestChildProviderAccountingOwnershipRetryAndReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "accounting.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initializeAccountingSession(t, store, "source", "/workspace")
	saveAccountingReceipt(t, store, "source", "request")
	entry := childAccountingEnvelope("request")
	want := entry.Clone()
	clone := entry.Clone()
	clone.PayloadJSON[0] = '!'
	if !bytes.Equal(entry.PayloadJSON, want.PayloadJSON) {
		t.Fatal("Clone aliases original")
	}
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{entry}); err != nil {
		t.Fatal(err)
	}
	entry.PayloadJSON[0] = '!'
	got, err := store.GetSessionChildProviderAccounting(t.Context(), "source", "request", 1)
	if err != nil || !reflect.DeepEqual(got.Accounting, want) || got.Sequence == 0 {
		t.Fatalf("stored envelope: %+v, %v", got, err)
	}
	sequence := got.Sequence
	got.Accounting.PayloadJSON[0] = '!'
	page, err := store.ListSessionChildProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0].Accounting, want) {
		t.Fatalf("page aliases Get: %v", err)
	}
	page[0].Accounting.PayloadJSON[0] = '!'
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{want, want}); err != nil {
		t.Fatal(err)
	}
	changed := want.Clone()
	changed.PayloadJSON = []byte(`{"opaque":"different"}`)
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{changed}); !errors.Is(err, db.ErrChildProviderAccountingConflict) {
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
	got, err = reopened.GetSessionChildProviderAccounting(t.Context(), "source", "request", 1)
	if err != nil || got.Sequence != sequence || !reflect.DeepEqual(got.Accounting, want) {
		t.Fatalf("reopen changed bytes/cursor: %+v, %v", got, err)
	}
	if _, err := reopened.GetSessionChildProviderAccounting(t.Context(), "other", "request", 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cross-session lookup: %v", err)
	}
}

func TestChildProviderAccountingInputAndPaginationBounds(t *testing.T) {
	store, _ := historyStore(t)
	saveAccountingReceipt(t, store, "source", "request")
	for _, tc := range []struct {
		name    string
		session string
		entry   db.SessionChildProviderAccounting
		want    error
	}{
		{"empty scope", "", childAccountingEnvelope("request"), db.ErrChildProviderAccountingInvalidRequest},
		{"empty receipt", "source", childAccountingEnvelope(""), db.ErrChildProviderAccountingInvalidRequest},
		{"missing receipt", "source", childAccountingEnvelope("absent"), db.ErrChildProviderAccountingInvalidRequest},
		{"wrong scope", "other", childAccountingEnvelope("request"), db.ErrChildProviderAccountingInvalidRequest},
		{"zero version", "source", db.SessionChildProviderAccounting{OwnerRequestBatchID: "request", ChildTaskID: "request", Attempt: 1, PayloadJSON: []byte(`{}`)}, db.ErrChildProviderAccountingVersion},
		{"unsupported version", "source", db.SessionChildProviderAccounting{OwnerRequestBatchID: "request", ChildTaskID: "request", Attempt: 1, Version: math.MaxUint64, PayloadJSON: []byte(`{}`)}, db.ErrChildProviderAccountingVersion},
		{"empty JSON", "source", db.SessionChildProviderAccounting{OwnerRequestBatchID: "request", ChildTaskID: "request", Attempt: 1, Version: 1}, db.ErrChildProviderAccountingInvalidRequest},
		{"invalid JSON", "source", db.SessionChildProviderAccounting{OwnerRequestBatchID: "request", ChildTaskID: "request", Attempt: 1, Version: 1, PayloadJSON: []byte(`CANARY_PRIVATE_PAYLOAD`)}, db.ErrChildProviderAccountingInvalidRequest},
		{"trailing JSON", "source", db.SessionChildProviderAccounting{OwnerRequestBatchID: "request", ChildTaskID: "request", Attempt: 1, Version: 1, PayloadJSON: []byte(`{} {}`)}, db.ErrChildProviderAccountingInvalidRequest},
		{"invalid UTF8", "source", db.SessionChildProviderAccounting{OwnerRequestBatchID: "request", ChildTaskID: "request", Attempt: 1, Version: 1, PayloadJSON: []byte{'"', 0xff, '"'}}, db.ErrChildProviderAccountingInvalidRequest},
		{"over limit", "source", db.SessionChildProviderAccounting{OwnerRequestBatchID: "request", ChildTaskID: "request", Attempt: 1, Version: 1, PayloadJSON: []byte(`"` + strings.Repeat("x", (1<<20)-1) + `"`)}, db.ErrChildProviderAccountingInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := store.SaveSessionChildProviderAccounting(t.Context(), tc.session, []db.SessionChildProviderAccounting{tc.entry})
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
		if _, err := store.ListSessionChildProviderAccounting(t.Context(), tc.session, tc.cursor, tc.limit); !errors.Is(err, db.ErrChildProviderAccountingInvalidRequest) {
			t.Fatalf("pagination bounds: %v", err)
		}
	}
	if _, err := store.GetSessionChildProviderAccounting(t.Context(), "", "request", 1); !errors.Is(err, db.ErrChildProviderAccountingInvalidRequest) {
		t.Fatalf("Get scope: %v", err)
	}
	if _, err := store.GetSessionChildProviderAccounting(t.Context(), "source", "", 1); !errors.Is(err, db.ErrChildProviderAccountingInvalidRequest) {
		t.Fatalf("Get key: %v", err)
	}
	entry := childAccountingEnvelope("request")
	entry.PayloadJSON = []byte(`"` + strings.Repeat("x", (1<<20)-2) + `"`)
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{entry}); err != nil {
		t.Fatalf("exact 1 MiB JSON (application schema is opaque): %v", err)
	}
	got, err := store.GetSessionChildProviderAccounting(t.Context(), "source", "request", 1)
	if err != nil || !bytes.Equal(got.Accounting.PayloadJSON, entry.PayloadJSON) {
		t.Fatalf("1 MiB round trip: %v", err)
	}
	page, err := store.ListSessionChildProviderAccounting(t.Context(), "source", math.MaxInt64, 1)
	if err != nil || len(page) != 0 {
		t.Fatalf("max valid cursor: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.SaveSessionChildProviderAccounting(ctx, "source", []db.SessionChildProviderAccounting{entry}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
}

func TestChildProviderAccountingIndependentStoreConcurrency(t *testing.T) {
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
				entry := childAccountingEnvelope(batch)
				if different {
					entry.PayloadJSON = fmt.Appendf(nil, `{"writer":%d}`, i)
				}
				wg.Go(func() {
					<-start
					errs <- store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{entry})
				})
			}
			close(start)
			wg.Wait()
			close(errs)
			successes := 0
			for err := range errs {
				if err == nil {
					successes++
				} else if !different || !errors.Is(err, db.ErrChildProviderAccountingConflict) {
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
	page, err := first.ListSessionChildProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 2 {
		t.Fatalf("concurrent saves duplicated receipts: %v", err)
	}
}

func TestChildProviderAccountingAtomicSaveAndTransactionBoundStore(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	for _, batch := range []string{"existing", "first", "bound"} {
		saveAccountingReceipt(t, store, "source", batch)
	}
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{childAccountingEnvelope("existing")}); err != nil {
		t.Fatal(err)
	}
	changed := childAccountingEnvelope("existing")
	changed.PayloadJSON = []byte(`{"changed":true}`)
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{childAccountingEnvelope("first"), changed}); !errors.Is(err, db.ErrChildProviderAccountingConflict) {
		t.Fatalf("atomic conflict: %v", err)
	}
	if _, err := store.GetSessionChildProviderAccounting(t.Context(), "source", "first", 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial batch escaped: %v", err)
	}
	rollback := errors.New("caller rollback")
	if err := store.WithTx(t.Context(), func(tx *db.Store) error {
		if err := tx.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{childAccountingEnvelope("bound")}); err != nil {
			return err
		}
		if _, err := tx.GetSessionChildProviderAccounting(t.Context(), "source", "bound", 1); err != nil {
			return err
		}
		if rows, err := tx.ListSessionChildProviderAccounting(t.Context(), "source", 0, 256); err != nil || len(rows) != 2 {
			t.Fatalf("transaction-bound read: %v", err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("transaction-bound save: %v", err)
	}
	if _, err := store.GetSessionChildProviderAccounting(t.Context(), "source", "bound", 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("caller rollback escaped: %v", err)
	}
	if err := store.WithTx(t.Context(), func(tx *db.Store) error {
		return tx.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{childAccountingEnvelope("bound")})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestChildProviderAccountingKeysetGapsForeignKeyAndCascade(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	initializeAccountingSession(t, store, "other", "/other")
	var sequences []uint64
	for _, session := range []string{"source", "other", "source", "source"} {
		batch := fmt.Sprintf("request-%d", len(sequences))
		saveAccountingReceipt(t, store, session, batch)
		if err := store.SaveSessionChildProviderAccounting(t.Context(), session, []db.SessionChildProviderAccounting{childAccountingEnvelope(batch)}); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetSessionChildProviderAccounting(t.Context(), session, batch, 1)
		if err != nil {
			t.Fatal(err)
		}
		sequences = append(sequences, got.Sequence)
	}
	if _, err := store.DB().ExecContext(t.Context(), "DELETE FROM session_history_batches WHERE session_id = ? AND batch_id = ?", "source", "request-2"); err != nil {
		t.Fatal(err)
	}
	first, err := store.ListSessionChildProviderAccounting(t.Context(), "source", 0, 1)
	if err != nil || len(first) != 1 || first[0].Sequence != sequences[0] {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := store.ListSessionChildProviderAccounting(t.Context(), "source", first[0].Sequence, 1)
	if err != nil || len(second) != 1 || second[0].Sequence != sequences[3] {
		t.Fatalf("page skips opaque gaps: %+v, %v", second, err)
	}
	last, err := store.ListSessionChildProviderAccounting(t.Context(), "source", second[0].Sequence, 1)
	if err != nil || len(last) != 0 {
		t.Fatalf("end page: %v", err)
	}
	if _, err := store.GetSessionChildProviderAccounting(t.Context(), "source", "request-2", 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("receipt deletion did not cascade: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO session_child_provider_accounting
		(session_id, owner_request_batch_id, child_task_id, attempt, version, payload_json, checksum) VALUES ('source', 'request-1', 'task', 1, 1, X'7b7d', 'invalid')`); err == nil {
		t.Fatal("FK accepted another session's receipt")
	}
	if err := store.DeleteSession(t.Context(), "source"); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListSessionChildProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 0 {
		t.Fatalf("session deletion did not cascade: %v", err)
	}
	page, err = store.ListSessionChildProviderAccounting(t.Context(), "other", 0, 256)
	if err != nil || len(page) != 1 {
		t.Fatalf("session deletion affected other scope: %v", err)
	}
}

func TestChildProviderAccountingStoredCorruptionIsSafe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		column string
		value  any
		want   error
	}{
		{"owner", "owner_request_batch_id", "request-2", db.ErrChildProviderAccountingCorrupt},
		{"task", "child_task_id", "changed-task", db.ErrChildProviderAccountingCorrupt},
		{"attempt", "attempt", 2, db.ErrChildProviderAccountingCorrupt},
		{"attempt wrong type", "attempt", "CANARY_PRIVATE_PAYLOAD", db.ErrChildProviderAccountingCorrupt},
		{"attempt zero", "attempt", 0, db.ErrChildProviderAccountingCorrupt},
		{"checksum", "checksum", "CANARY_PRIVATE_PAYLOAD", db.ErrChildProviderAccountingCorrupt},
		{"changed valid JSON", "payload_json", []byte(`{"canary":"CANARY_PRIVATE_PAYLOAD"}`), db.ErrChildProviderAccountingCorrupt},
		{"malformed JSON", "payload_json", []byte(`CANARY_PRIVATE_PAYLOAD`), db.ErrChildProviderAccountingCorrupt},
		{"invalid UTF8", "payload_json", []byte{'"', 0xff, '"'}, db.ErrChildProviderAccountingCorrupt},
		{"oversized JSON", "payload_json", []byte(`"` + strings.Repeat("x", 1<<20) + `"`), db.ErrChildProviderAccountingCorrupt},
		{"payload wrong type", "payload_json", "CANARY_PRIVATE_PAYLOAD", db.ErrChildProviderAccountingCorrupt},
		{"version wrong type", "version", "CANARY_PRIVATE_PAYLOAD", db.ErrChildProviderAccountingCorrupt},
		{"version negative", "version", -1, db.ErrChildProviderAccountingCorrupt},
		{"version zero", "version", 0, db.ErrChildProviderAccountingCorrupt},
		{"version unsupported", "version", 2, db.ErrChildProviderAccountingVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, _ := historyStore(t)
			saveAccountingReceipt(t, store, "source", "request")
			saveAccountingReceipt(t, store, "source", "request-2")
			entry := childAccountingEnvelope("request")
			if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{entry}); err != nil {
				t.Fatal(err)
			}
			// Column identifiers come only from this fixed test table.
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_child_provider_accounting SET "+tc.column+" = ?", tc.value); err != nil {
				t.Fatal(err)
			}
			task, attempt := "request", 1
			if tc.column == "child_task_id" {
				task = "changed-task"
				entry.ChildTaskID = task
			}
			if tc.column == "attempt" { // Invalid attempt cannot be addressed through the public key boundary.
				page, err := store.ListSessionChildProviderAccounting(t.Context(), "source", 0, 256)
				if !errors.Is(err, tc.want) || page != nil {
					t.Fatalf("invalid stored attempt: %v", err)
				}
				return
			}
			_, getErr := store.GetSessionChildProviderAccounting(t.Context(), "source", task, attempt)
			page, listErr := store.ListSessionChildProviderAccounting(t.Context(), "source", 0, 256)
			saveErr := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{entry})
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
