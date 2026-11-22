package db_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func commitUnversionedAccounting(ctx context.Context, f versionedAccountingFixture, store *db.Store, roots []db.SessionProviderAccounting, children []db.SessionChildProviderAccounting, history ...db.SessionHistoryBatch) error {
	if f.mode == "completed" || f.mode == "completed-delta" {
		return store.CommitCompletedTurnWithAccounting(ctx, f.record, f.update, roots, children, history...)
	}
	return store.CommitCheckpointTurnWithAccounting(ctx, f.record, f.update, f.expected, roots, children, history...)
}

func TestUnversionedAccountingAtomicFailureAndRetry(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"completed", "completed-delta", "checkpoint", "checkpoint-delta"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			root, child, batch := accountingEnvelope("owner"), childAccountingEnvelope("owner"), versionedAccountingBatch("owner")
			changed := child.Clone()
			changed.PayloadJSON = []byte(`{"changed":true}`)
			for _, tc := range []struct {
				name     string
				roots    []db.SessionProviderAccounting
				children []db.SessionChildProviderAccounting
				history  []db.SessionHistoryBatch
				want     error
			}{
				{"child conflict", []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child, changed}, []db.SessionHistoryBatch{batch}, db.ErrChildProviderAccountingConflict},
				{"missing child receipt", nil, []db.SessionChildProviderAccounting{child}, nil, db.ErrChildProviderAccountingInvalidRequest},
				{"missing root receipt", []db.SessionProviderAccounting{root}, nil, nil, db.ErrProviderAccountingInvalidRequest},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if err := commitUnversionedAccounting(t.Context(), f, f.store, tc.roots, tc.children, tc.history...); !errors.Is(err, tc.want) {
						t.Fatalf("commit: %v", err)
					}
					assertVersionedAccountingUnchanged(t, f, before)
					if childAccountingCount(t, f.store) != 0 {
						t.Fatal("child escaped rollback")
					}
				})
			}
			if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_child BEFORE INSERT ON session_child_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
				t.Fatal(err)
			}
			if err := commitUnversionedAccounting(t.Context(), f, f.store, []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child}, batch); err == nil {
				t.Fatal("trigger accepted")
			}
			assertVersionedAccountingUnchanged(t, f, before)
			if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_child`); err != nil {
				t.Fatal(err)
			}
			if err := commitUnversionedAccounting(t.Context(), f, f.store, []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child}, batch); err != nil {
				t.Fatal(err)
			}
			got, err := f.store.GetSessionChildProviderAccounting(t.Context(), f.record.ID, child.ChildTaskID, child.Attempt)
			if err != nil || !reflect.DeepEqual(got.Accounting, child) {
				t.Fatal("child not atomic", err)
			}
			f.expected, err = f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.update.ExpectedRevision, f.update.Entries = f.update.Revision, nil
			if err := commitUnversionedAccounting(t.Context(), f, f.store, []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child, child}, batch); err != nil {
				t.Fatal(err)
			}
			again, err := f.store.GetSessionChildProviderAccounting(t.Context(), f.record.ID, child.ChildTaskID, child.Attempt)
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatal("retry changed cursor/payload", err)
			}
			model, err := f.store.GetSessionModelContext(t.Context(), f.record.ID)
			if err != nil || model.Generation != 1 {
				t.Fatal("retry duplicated history", err)
			}
		})
	}
}

func TestUnversionedAccountingGuardsAndBorrowedTransaction(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"completed-delta", "checkpoint-delta"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			root, child, batch := accountingEnvelope("owner"), childAccountingEnvelope("owner"), versionedAccountingBatch("owner")
			wrong := f
			wrong.record.ID = "another-session"
			if err := commitUnversionedAccounting(t.Context(), wrong, f.store, nil, []db.SessionChildProviderAccounting{child}, batch); !errors.Is(err, db.ErrChildProviderAccountingInvalidRequest) {
				t.Fatal("cross session", err)
			}
			wrong = f
			wrong.update.ExpectedRevision++
			if err := commitUnversionedAccounting(t.Context(), wrong, f.store, nil, []db.SessionChildProviderAccounting{child}, batch); err == nil {
				t.Fatal("stale transcript accepted")
			}
			if mode == "checkpoint-delta" {
				wrong = f
				wrong.expected = db.SessionContentVersion{}
				if err := commitUnversionedAccounting(t.Context(), wrong, f.store, nil, []db.SessionChildProviderAccounting{child}, batch); !errors.Is(err, db.ErrCheckpointConflict) {
					t.Fatal("stale source", err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := commitUnversionedAccounting(ctx, f, f.store, nil, []db.SessionChildProviderAccounting{child}, batch); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity", err)
			}
			rollback := errors.New("caller rollback")
			if err := f.store.WithTx(t.Context(), func(tx *db.Store) error {
				if err := commitUnversionedAccounting(t.Context(), f, tx, []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child}, batch); err != nil {
					return err
				}
				if _, err := tx.GetSessionChildProviderAccounting(t.Context(), f.record.ID, child.ChildTaskID, child.Attempt); err != nil {
					return err
				}
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal("borrowed transaction", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			if childAccountingCount(t, f.store) != 0 {
				t.Fatal("borrowed commit escaped rollback")
			}
			if err := f.store.WithTx(t.Context(), func(tx *db.Store) error {
				return commitUnversionedAccounting(t.Context(), f, tx, []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child}, batch)
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnversionedAccountingCommitTimeFailure(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"completed-delta", "checkpoint-delta"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			// A deferred foreign key permits all mutators to succeed and rejects only
			// transaction commit. The production operation must roll back every set.
			if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TABLE deferred_accounting_guard (id TEXT REFERENCES sessions(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER defer_child AFTER INSERT ON session_child_provider_accounting BEGIN INSERT INTO deferred_accounting_guard VALUES ('missing-session'); END`); err != nil {
				t.Fatal(err)
			}
			if err := commitUnversionedAccounting(t.Context(), f, f.store, []db.SessionProviderAccounting{accountingEnvelope("owner")}, []db.SessionChildProviderAccounting{childAccountingEnvelope("owner")}, versionedAccountingBatch("owner")); err == nil {
				t.Fatal("deferred constraint accepted")
			}
			assertVersionedAccountingUnchanged(t, f, before)
			if childAccountingCount(t, f.store) != 0 {
				t.Fatal("commit failure leaked children")
			}
			if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER defer_child`); err != nil {
				t.Fatal(err)
			}
			if err := commitUnversionedAccounting(t.Context(), f, f.store, nil, nil); err != nil {
				t.Fatal("nil-child compatibility", err)
			}
		})
	}
}
