package db_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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

func auxiliaryAccountingEnvelope(batch string) db.SessionAuxiliaryProviderAccounting {
	return db.SessionAuxiliaryProviderAccounting{OwnerRequestBatchID: batch, InvocationID: batch, Version: 1, PayloadJSON: []byte(`{"opaque":"settled"}`)}
}

func TestAuxiliaryProviderAccountingOwnershipRetryAndReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "accounting.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initializeAccountingSession(t, store, "source", "/workspace")
	saveAccountingReceipt(t, store, "source", "request")
	entry := auxiliaryAccountingEnvelope("request")
	want := entry.Clone()
	clone := entry.Clone()
	clone.PayloadJSON[0] = '!'
	if !bytes.Equal(entry.PayloadJSON, want.PayloadJSON) {
		t.Fatal("Clone aliases original")
	}
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{entry}); err != nil {
		t.Fatal(err)
	}
	entry.PayloadJSON[0] = '!'
	got, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", "request")
	if err != nil || !reflect.DeepEqual(got.Accounting, want) || got.Sequence == 0 {
		t.Fatalf("stored envelope: %+v, %v", got, err)
	}
	sequence := got.Sequence
	got.Accounting.PayloadJSON[0] = '!'
	page, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0].Accounting, want) {
		t.Fatalf("page aliases Get: %v", err)
	}
	page[0].Accounting.PayloadJSON[0] = '!'
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{want, want}); err != nil {
		t.Fatal(err)
	}
	changed := want.Clone()
	changed.PayloadJSON = []byte(`{"opaque":"different"}`)
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{changed}); !errors.Is(err, db.ErrAuxiliaryProviderAccountingConflict) {
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
	got, err = reopened.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", "request")
	if err != nil || got.Sequence != sequence || !reflect.DeepEqual(got.Accounting, want) {
		t.Fatalf("reopen changed bytes/cursor: %+v, %v", got, err)
	}
	if _, err := reopened.GetSessionAuxiliaryProviderAccounting(t.Context(), "other", "request"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cross-session lookup: %v", err)
	}
}

func TestAuxiliaryProviderAccountingInputAndPaginationBounds(t *testing.T) {
	store, _ := historyStore(t)
	saveAccountingReceipt(t, store, "source", "request")
	for _, tc := range []struct {
		name    string
		session string
		entry   db.SessionAuxiliaryProviderAccounting
		want    error
	}{
		{"empty scope", "", auxiliaryAccountingEnvelope("request"), db.ErrAuxiliaryProviderAccountingInvalidRequest},
		{"empty receipt", "source", auxiliaryAccountingEnvelope(""), db.ErrAuxiliaryProviderAccountingInvalidRequest},
		{"missing receipt", "source", auxiliaryAccountingEnvelope("absent"), db.ErrAuxiliaryProviderAccountingInvalidRequest},
		{"wrong scope", "other", auxiliaryAccountingEnvelope("request"), db.ErrAuxiliaryProviderAccountingInvalidRequest},
		{"zero version", "source", db.SessionAuxiliaryProviderAccounting{OwnerRequestBatchID: "request", InvocationID: "request", PayloadJSON: []byte(`{}`)}, db.ErrAuxiliaryProviderAccountingVersion},
		{"unsupported version", "source", db.SessionAuxiliaryProviderAccounting{OwnerRequestBatchID: "request", InvocationID: "request", Version: math.MaxUint64, PayloadJSON: []byte(`{}`)}, db.ErrAuxiliaryProviderAccountingVersion},
		{"empty JSON", "source", db.SessionAuxiliaryProviderAccounting{OwnerRequestBatchID: "request", InvocationID: "request", Version: 1}, db.ErrAuxiliaryProviderAccountingInvalidRequest},
		{"invalid JSON", "source", db.SessionAuxiliaryProviderAccounting{OwnerRequestBatchID: "request", InvocationID: "request", Version: 1, PayloadJSON: []byte(`CANARY_PRIVATE_PAYLOAD`)}, db.ErrAuxiliaryProviderAccountingInvalidRequest},
		{"trailing JSON", "source", db.SessionAuxiliaryProviderAccounting{OwnerRequestBatchID: "request", InvocationID: "request", Version: 1, PayloadJSON: []byte(`{} {}`)}, db.ErrAuxiliaryProviderAccountingInvalidRequest},
		{"invalid UTF8", "source", db.SessionAuxiliaryProviderAccounting{OwnerRequestBatchID: "request", InvocationID: "request", Version: 1, PayloadJSON: []byte{'"', 0xff, '"'}}, db.ErrAuxiliaryProviderAccountingInvalidRequest},
		{"over limit", "source", db.SessionAuxiliaryProviderAccounting{OwnerRequestBatchID: "request", InvocationID: "request", Version: 1, PayloadJSON: []byte(`"` + strings.Repeat("x", (1<<20)-1) + `"`)}, db.ErrAuxiliaryProviderAccountingInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), tc.session, []db.SessionAuxiliaryProviderAccounting{tc.entry})
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
		if _, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), tc.session, tc.cursor, tc.limit); !errors.Is(err, db.ErrAuxiliaryProviderAccountingInvalidRequest) {
			t.Fatalf("pagination bounds: %v", err)
		}
	}
	if _, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "", "request"); !errors.Is(err, db.ErrAuxiliaryProviderAccountingInvalidRequest) {
		t.Fatalf("Get scope: %v", err)
	}
	if _, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", ""); !errors.Is(err, db.ErrAuxiliaryProviderAccountingInvalidRequest) {
		t.Fatalf("Get key: %v", err)
	}
	entry := auxiliaryAccountingEnvelope("request")
	entry.PayloadJSON = []byte(`"` + strings.Repeat("x", (1<<20)-2) + `"`)
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{entry}); err != nil {
		t.Fatalf("exact 1 MiB JSON (application schema is opaque): %v", err)
	}
	got, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", "request")
	if err != nil || !bytes.Equal(got.Accounting.PayloadJSON, entry.PayloadJSON) {
		t.Fatalf("1 MiB round trip: %v", err)
	}
	page, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", math.MaxInt64, 1)
	if err != nil || len(page) != 0 {
		t.Fatalf("max valid cursor: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.SaveSessionAuxiliaryProviderAccounting(ctx, "source", []db.SessionAuxiliaryProviderAccounting{entry}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
}

func TestAuxiliaryProviderAccountingIndependentStoreConcurrency(t *testing.T) {
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
				entry := auxiliaryAccountingEnvelope(batch)
				if different {
					entry.PayloadJSON = fmt.Appendf(nil, `{"writer":%d}`, i)
				}
				wg.Go(func() {
					<-start
					errs <- store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{entry})
				})
			}
			close(start)
			wg.Wait()
			close(errs)
			successes := 0
			for err := range errs {
				if err == nil {
					successes++
				} else if !different || !errors.Is(err, db.ErrAuxiliaryProviderAccountingConflict) {
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
	page, err := first.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 2 {
		t.Fatalf("concurrent saves duplicated receipts: %v", err)
	}
}

func TestAuxiliaryProviderAccountingAtomicSaveAndTransactionBoundStore(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	for _, batch := range []string{"existing", "first", "bound"} {
		saveAccountingReceipt(t, store, "source", batch)
	}
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{auxiliaryAccountingEnvelope("existing")}); err != nil {
		t.Fatal(err)
	}
	changed := auxiliaryAccountingEnvelope("existing")
	changed.PayloadJSON = []byte(`{"changed":true}`)
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{auxiliaryAccountingEnvelope("first"), changed}); !errors.Is(err, db.ErrAuxiliaryProviderAccountingConflict) {
		t.Fatalf("atomic conflict: %v", err)
	}
	if _, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", "first"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial batch escaped: %v", err)
	}
	rollback := errors.New("caller rollback")
	if err := store.WithTx(t.Context(), func(tx *db.Store) error {
		if err := tx.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{auxiliaryAccountingEnvelope("bound")}); err != nil {
			return err
		}
		if _, err := tx.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", "bound"); err != nil {
			return err
		}
		if rows, err := tx.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256); err != nil || len(rows) != 2 {
			t.Fatalf("transaction-bound read: %v", err)
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("transaction-bound save: %v", err)
	}
	if _, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", "bound"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("caller rollback escaped: %v", err)
	}
	if err := store.WithTx(t.Context(), func(tx *db.Store) error {
		return tx.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{auxiliaryAccountingEnvelope("bound")})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAuxiliaryProviderAccountingKeysetGapsForeignKeyAndCascade(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	initializeAccountingSession(t, store, "other", "/other")
	var sequences []uint64
	for _, session := range []string{"source", "other", "source", "source"} {
		batch := fmt.Sprintf("request-%d", len(sequences))
		saveAccountingReceipt(t, store, session, batch)
		if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), session, []db.SessionAuxiliaryProviderAccounting{auxiliaryAccountingEnvelope(batch)}); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), session, batch)
		if err != nil {
			t.Fatal(err)
		}
		sequences = append(sequences, got.Sequence)
	}
	if _, err := store.DB().ExecContext(t.Context(), "DELETE FROM session_history_batches WHERE session_id = ? AND batch_id = ?", "source", "request-2"); err != nil {
		t.Fatal(err)
	}
	first, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 1)
	if err != nil || len(first) != 1 || first[0].Sequence != sequences[0] {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", first[0].Sequence, 1)
	if err != nil || len(second) != 1 || second[0].Sequence != sequences[3] {
		t.Fatalf("page skips opaque gaps: %+v, %v", second, err)
	}
	last, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", second[0].Sequence, 1)
	if err != nil || len(last) != 0 {
		t.Fatalf("end page: %v", err)
	}
	if _, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", "request-2"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("receipt deletion did not cascade: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO session_auxiliary_provider_accounting
		(session_id, owner_request_batch_id, invocation_id, version, payload_json, checksum) VALUES ('source', 'request-1', 'task', 1, X'7b7d', 'invalid')`); err == nil {
		t.Fatal("FK accepted another session's receipt")
	}
	if err := store.DeleteSession(t.Context(), "source"); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(page) != 0 {
		t.Fatalf("session deletion did not cascade: %v", err)
	}
	page, err = store.ListSessionAuxiliaryProviderAccounting(t.Context(), "other", 0, 256)
	if err != nil || len(page) != 1 {
		t.Fatalf("session deletion affected other scope: %v", err)
	}
}

func TestAuxiliaryProviderAccountingStoredCorruptionIsSafe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		column string
		value  any
		want   error
	}{
		{"owner wrong type", "owner_request_batch_id", []byte("request"), db.ErrAuxiliaryProviderAccountingCorrupt},
		{"invocation wrong type", "invocation_id", []byte("request"), db.ErrAuxiliaryProviderAccountingCorrupt},
		{"checksum wrong type", "checksum", []byte("PRIVATE-CANARY"), db.ErrAuxiliaryProviderAccountingCorrupt},
		{"owner", "owner_request_batch_id", "request-2", db.ErrAuxiliaryProviderAccountingCorrupt},
		{"task", "invocation_id", "changed-task", db.ErrAuxiliaryProviderAccountingCorrupt},
		{"checksum", "checksum", "CANARY_PRIVATE_PAYLOAD", db.ErrAuxiliaryProviderAccountingCorrupt},
		{"changed valid JSON", "payload_json", []byte(`{"canary":"CANARY_PRIVATE_PAYLOAD"}`), db.ErrAuxiliaryProviderAccountingCorrupt},
		{"malformed JSON", "payload_json", []byte(`CANARY_PRIVATE_PAYLOAD`), db.ErrAuxiliaryProviderAccountingCorrupt},
		{"invalid UTF8", "payload_json", []byte{'"', 0xff, '"'}, db.ErrAuxiliaryProviderAccountingCorrupt},
		{"oversized JSON", "payload_json", []byte(`"` + strings.Repeat("x", 1<<20) + `"`), db.ErrAuxiliaryProviderAccountingCorrupt},
		{"payload wrong type", "payload_json", "CANARY_PRIVATE_PAYLOAD", db.ErrAuxiliaryProviderAccountingCorrupt},
		{"version fractional", "version", 1.5, db.ErrAuxiliaryProviderAccountingCorrupt},
		{"version wrong type", "version", "CANARY_PRIVATE_PAYLOAD", db.ErrAuxiliaryProviderAccountingCorrupt},
		{"version negative", "version", -1, db.ErrAuxiliaryProviderAccountingCorrupt},
		{"version zero", "version", 0, db.ErrAuxiliaryProviderAccountingCorrupt},
		{"version unsupported", "version", 2, db.ErrAuxiliaryProviderAccountingVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, _ := historyStore(t)
			saveAccountingReceipt(t, store, "source", "request")
			saveAccountingReceipt(t, store, "source", "request-2")
			entry := auxiliaryAccountingEnvelope("request")
			if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{entry}); err != nil {
				t.Fatal(err)
			}
			// Only this isolated test database bypasses FK enforcement to model
			// an externally corrupted owner storage type.
			if tc.name == "owner wrong type" {
				if _, err := store.DB().ExecContext(t.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
					t.Fatal(err)
				}
			}
			// Column identifiers come only from this fixed test table.
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_auxiliary_provider_accounting SET "+tc.column+" = ?", tc.value); err != nil {
				t.Fatal(err)
			}
			if tc.name == "invocation wrong type" {
				page, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256)
				if !errors.Is(err, tc.want) || page != nil {
					t.Fatal("ill-typed invocation", err)
				}
				return
			}
			invocation := "request"
			if tc.column == "invocation_id" {
				invocation = "changed-task"
				entry.InvocationID = invocation
			}
			_, getErr := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", invocation)
			page, listErr := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256)
			saveErr := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{entry})
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

func TestAuxiliaryProviderAccountingIdentityAndWholePageIntegrity(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	for _, owner := range []string{"owner", "other-owner"} {
		saveAccountingReceipt(t, store, "source", owner)
	}
	entry := auxiliaryAccountingEnvelope("owner")
	// DB IDs are deliberately opaque; UUID grammar belongs to the engine codec.
	entry.InvocationID = " opaque / invocation 😀 "
	another := entry.Clone()
	another.InvocationID += "distinct"
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{entry, another}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(rows) != 2 || rows[0].Sequence >= rows[1].Sequence {
		t.Fatal("distinct invocations collapsed", err)
	}
	changed := entry.Clone()
	changed.OwnerRequestBatchID = "other-owner"
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{changed}); !errors.Is(err, db.ErrAuxiliaryProviderAccountingConflict) {
		t.Fatal("changed owner", err)
	}
	changed = entry.Clone()
	changed.PayloadJSON = append(changed.PayloadJSON, ' ')
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{changed}); !errors.Is(err, db.ErrAuxiliaryProviderAccountingConflict) {
		t.Fatal("exact bytes not immutable", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_auxiliary_provider_accounting SET checksum = 'PRIVATE-CANARY' WHERE invocation_id = ?", another.InvocationID); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256)
	if !errors.Is(err, db.ErrAuxiliaryProviderAccountingCorrupt) || page != nil || errors.Unwrap(err) != nil {
		t.Fatal("partial page or unsafe error", err)
	}
}

func TestAuxiliaryProviderAccountingChecksumScopeAndFraming(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	initializeAccountingSession(t, store, "target", "/other")
	for _, session := range []string{"source", "target"} {
		saveAccountingReceipt(t, store, session, "owner")
	}
	entry := auxiliaryAccountingEnvelope("owner")
	if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), "source", []db.SessionAuxiliaryProviderAccounting{entry}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT checksum FROM session_auxiliary_provider_accounting").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var framed []byte
	for _, field := range [][]byte{[]byte("source"), []byte(entry.InvocationID), []byte(entry.OwnerRequestBatchID), binary.BigEndian.AppendUint64(nil, 1), entry.PayloadJSON} {
		framed = binary.BigEndian.AppendUint64(framed, uint64(len(field)))
		framed = append(framed, field...)
	}
	digest := sha256.Sum256(framed)
	if stored != hex.EncodeToString(digest[:]) {
		t.Fatal("checksum does not bind every length-framed field")
	}
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_auxiliary_provider_accounting SET session_id = 'target'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "target", entry.InvocationID); !errors.Is(err, db.ErrAuxiliaryProviderAccountingCorrupt) {
		t.Fatal("session transplant", err)
	}
}

func TestAuxiliaryProviderAccountingUTF8BoundariesAndCancellation(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	saveAccountingReceipt(t, store, "source", "owner")
	invalid := string([]byte{0xff})
	for _, field := range []string{"session", "invocation", "owner"} {
		session := "source"
		entry := auxiliaryAccountingEnvelope("owner")
		switch field {
		case "session":
			session = invalid
		case "invocation":
			entry.InvocationID = invalid
		case "owner":
			entry.OwnerRequestBatchID = invalid
		}
		if err := store.SaveSessionAuxiliaryProviderAccounting(t.Context(), session, []db.SessionAuxiliaryProviderAccounting{entry}); !errors.Is(err, db.ErrAuxiliaryProviderAccountingInvalidRequest) {
			t.Fatal(field, err)
		}
	}
	if _, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), invalid, "owner"); !errors.Is(err, db.ErrAuxiliaryProviderAccountingInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := store.GetSessionAuxiliaryProviderAccounting(t.Context(), "source", invalid); !errors.Is(err, db.ErrAuxiliaryProviderAccountingInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), invalid, 0, 1); !errors.Is(err, db.ErrAuxiliaryProviderAccountingInvalidRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.GetSessionAuxiliaryProviderAccounting(ctx, "source", "owner"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := store.ListSessionAuxiliaryProviderAccounting(ctx, "source", 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
