package db_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

func manualBatchCommit(t *testing.T, f versionedAccountingFixture, entries []db.SessionManualProviderAccounting) (db.SessionContentVersion, error) {
	t.Helper()
	batch := db.SessionAccountingBatch{Manual: entries}
	switch f.mode {
	case "completed", "completed-delta":
		return f.store.CommitCompletedTurnWithAccountingBatchVersioned(t.Context(), f.record, f.update, f.expected, batch)
	case "checkpoint", "checkpoint-delta":
		return f.store.CommitCheckpointTurnWithAccountingBatchVersioned(t.Context(), f.record, f.update, f.expected, batch)
	default:
		return f.store.CommitBeforeTurnWithAccountingBatchVersioned(t.Context(), f.record, f.update, f.boundary, "ignored", f.expected, batch)
	}
}

func TestManualAccountingAtomicBatch(t *testing.T) {
	for _, mode := range versionedAccountingModes {
		t.Run(mode, func(t *testing.T) {
			f := newVersionedAccountingFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			entry := db.SessionManualProviderAccounting{InvocationID: "manual", Version: 1, PayloadJSON: []byte(`{"usage":1}`)}
			changed := entry.Clone()
			changed.PayloadJSON = []byte(`{"usage":2}`)
			if version, err := manualBatchCommit(t, f, []db.SessionManualProviderAccounting{entry, changed}); !errors.Is(err, db.ErrManualProviderAccountingConflict) || version != (db.SessionContentVersion{}) {
				t.Fatalf("late conflict: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			if _, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "manual"); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("partial write: %v", err)
			}
			if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_manual BEFORE INSERT ON session_manual_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
				t.Fatal(err)
			}
			if version, err := manualBatchCommit(t, f, []db.SessionManualProviderAccounting{entry}); err == nil || version != (db.SessionContentVersion{}) {
				t.Fatalf("partial writer: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_manual`); err != nil {
				t.Fatal(err)
			}
			version, err := manualBatchCommit(t, f, []db.SessionManualProviderAccounting{entry})
			if err != nil || version == (db.SessionContentVersion{}) {
				t.Fatalf("commit: %v", err)
			}
			state, err := f.store.GetSessionResumeState(t.Context(), "source")
			if err != nil || state.ContentVersion != version {
				t.Fatalf("version: %v", err)
			}
			got, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "manual")
			if err != nil || !reflect.DeepEqual(got.Accounting, entry) {
				t.Fatalf("evidence: %v", err)
			}
			got.Accounting.PayloadJSON[0] = 'x'
			again, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "manual")
			if err != nil || !reflect.DeepEqual(again.Accounting, entry) {
				t.Fatalf("alias: %v", err)
			}
		})
	}
}

func TestManualAccountingScopeAndPagination(t *testing.T) {
	f := newVersionedAccountingFixture(t, "completed")
	entry := db.SessionManualProviderAccounting{InvocationID: "one", Version: 1, PayloadJSON: []byte(`{}`)}
	changed := entry.Clone()
	changed.InvocationID = "two"
	if _, err := manualBatchCommit(t, f, []db.SessionManualProviderAccounting{entry, changed}); err != nil {
		t.Fatal(err)
	}
	page, err := f.store.ListSessionManualProviderAccounting(t.Context(), "source", 0, 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("page: %v", err)
	}
	next, err := f.store.ListSessionManualProviderAccounting(t.Context(), "source", page[0].Sequence, 1)
	if err != nil || len(next) != 1 || next[0].Accounting.InvocationID != "two" {
		t.Fatalf("next: %v", err)
	}
	if _, err := f.store.ListSessionManualProviderAccounting(t.Context(), "source", 0, 0); !errors.Is(err, db.ErrManualProviderAccountingInvalidRequest) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := f.store.GetSessionManualProviderAccounting(t.Context(), "other", "one"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("scope: %v", err)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE session_manual_provider_accounting SET checksum='bad' WHERE invocation_id='two'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ListSessionManualProviderAccounting(t.Context(), "source", 0, 2); !errors.Is(err, db.ErrManualProviderAccountingCorrupt) {
		t.Fatalf("page corruption: %v", err)
	}
}

func TestManualAccountingMigration43(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	provider, err := migrations.NewProvider(s.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	if version, err := provider.GetDBVersion(t.Context()); err != nil || version != 42 {
		t.Fatalf("down: %d %v", version, err)
	}
	if _, err := provider.UpTo(t.Context(), 43); err != nil {
		t.Fatal(err)
	}
	if version, err := provider.GetDBVersion(t.Context()); err != nil || version != 43 {
		t.Fatalf("up: %d %v", version, err)
	}
}

func TestManualAccountingNonversionedAndGuards(t *testing.T) {
	for _, mode := range []string{"completed", "checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			f := newVersionedAccountingFixture(t, mode)
			entry := db.SessionManualProviderAccounting{InvocationID: "manual", Version: 1, PayloadJSON: []byte(`{"actual":1}`)}
			commit := func(batch db.SessionAccountingBatch) error {
				if mode == "completed" {
					return f.store.CommitCompletedTurnWithAccountingBatch(t.Context(), f.record, f.update, batch)
				}
				return f.store.CommitCheckpointTurnWithAccountingBatch(t.Context(), f.record, f.update, f.expected, batch)
			}
			wrong := f
			wrong.record.ID = "other"
			if err := wrong.store.CommitCompletedTurnWithAccountingBatch(t.Context(), wrong.record, wrong.update, db.SessionAccountingBatch{Manual: []db.SessionManualProviderAccounting{entry}}); !errors.Is(err, db.ErrManualProviderAccountingInvalidRequest) {
				t.Fatalf("cross-session: %v", err)
			}
			if err := commit(db.SessionAccountingBatch{Manual: []db.SessionManualProviderAccounting{entry}}); err != nil {
				t.Fatal(err)
			}
			first, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "manual")
			if err != nil {
				t.Fatal(err)
			}
			f.expected, err = f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := commit(db.SessionAccountingBatch{Manual: []db.SessionManualProviderAccounting{entry}}); err != nil {
				t.Fatalf("retry: %v", err)
			}
			second, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "manual")
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("cursor changed: %v", err)
			}
			f.expected, err = f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			other := entry.Clone()
			other.PayloadJSON = []byte(`{"actual":2}`)
			if err := commit(db.SessionAccountingBatch{Manual: []db.SessionManualProviderAccounting{other}}); !errors.Is(err, db.ErrManualProviderAccountingConflict) {
				t.Fatalf("conflict: %v", err)
			}
		})
	}
}

func TestManualAccountingInputAndStoredIntegrity(t *testing.T) {
	f := newVersionedAccountingFixture(t, "completed")
	entry := db.SessionManualProviderAccounting{InvocationID: "one", Version: 1, PayloadJSON: []byte(`{}`)}
	for _, tc := range []struct {
		entry db.SessionManualProviderAccounting
		want  error
	}{
		{db.SessionManualProviderAccounting{InvocationID: "one", Version: 2, PayloadJSON: []byte(`{}`)}, db.ErrManualProviderAccountingVersion},
		{db.SessionManualProviderAccounting{InvocationID: "one", Version: 1, PayloadJSON: []byte(`not-json`)}, db.ErrManualProviderAccountingInvalidRequest},
	} {
		if _, err := manualBatchCommit(t, f, []db.SessionManualProviderAccounting{tc.entry}); !errors.Is(err, tc.want) {
			t.Fatalf("validation: %v", err)
		}
	}
	if _, err := manualBatchCommit(t, f, []db.SessionManualProviderAccounting{entry}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE session_manual_provider_accounting SET version=2 WHERE invocation_id='one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "one"); !errors.Is(err, db.ErrManualProviderAccountingVersion) {
		t.Fatalf("unsupported stored version: %v", err)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE session_manual_provider_accounting SET version=1, payload_json='{}' WHERE invocation_id='one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "one"); !errors.Is(err, db.ErrManualProviderAccountingCorrupt) {
		t.Fatalf("wrong SQLite type: %v", err)
	}
}

func TestManualAccountingSessionCascade(t *testing.T) {
	f := newVersionedAccountingFixture(t, "completed")
	entry := db.SessionManualProviderAccounting{InvocationID: "one", Version: 1, PayloadJSON: []byte(`{}`)}
	if _, err := manualBatchCommit(t, f, []db.SessionManualProviderAccounting{entry}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `DELETE FROM sessions WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "one"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cascade: %v", err)
	}
}

func TestManualAccountingDeferredCommitRollback(t *testing.T) {
	f := newVersionedAccountingFixture(t, "completed")
	before := snapshotVersionedAccounting(t, f)
	if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TABLE manual_deferred_parent(id INTEGER PRIMARY KEY);
CREATE TABLE manual_deferred_child(parent INTEGER REFERENCES manual_deferred_parent(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER reject_manual_deferred AFTER INSERT ON session_manual_provider_accounting BEGIN INSERT INTO manual_deferred_child VALUES (99); END;`); err != nil {
		t.Fatal(err)
	}
	entry := db.SessionManualProviderAccounting{InvocationID: "deferred", Version: 1, PayloadJSON: []byte(`{}`)}
	version, err := manualBatchCommit(t, f, []db.SessionManualProviderAccounting{entry})
	if err == nil || version != (db.SessionContentVersion{}) {
		t.Fatalf("deferred failure: %v", err)
	}
	assertVersionedAccountingUnchanged(t, f, before)
	if _, err := f.store.GetSessionManualProviderAccounting(t.Context(), "source", "deferred"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial receipt: %v", err)
	}
}
