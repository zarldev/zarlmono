package db_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

var auxiliaryBatchModes = []string{
	"completed", "completed-delta", "checkpoint", "checkpoint-delta", "before-initial", "before-existing", "before-delta",
	"unversioned/completed", "unversioned/completed-delta", "unversioned/checkpoint", "unversioned/checkpoint-delta",
}

func newAuxiliaryBatchFixture(t *testing.T, mode string) versionedAccountingFixture {
	t.Helper()
	return newVersionedAccountingFixture(t, strings.TrimPrefix(mode, "unversioned/"))
}

func commitAuxiliaryBatch(ctx context.Context, f versionedAccountingFixture, mode string, store *db.Store, accounting db.SessionAccountingBatch, history ...db.SessionHistoryBatch) (db.SessionContentVersion, error) {
	if strings.HasPrefix(mode, "unversioned/") {
		if f.mode == "completed" || f.mode == "completed-delta" {
			return db.SessionContentVersion{}, store.CommitCompletedTurnWithAccountingBatch(ctx, f.record, f.update, accounting, history...)
		}
		return db.SessionContentVersion{}, store.CommitCheckpointTurnWithAccountingBatch(ctx, f.record, f.update, f.expected, accounting, history...)
	}
	switch f.mode {
	case "completed", "completed-delta":
		return store.CommitCompletedTurnWithAccountingBatchVersioned(ctx, f.record, f.update, f.expected, accounting, history...)
	case "checkpoint", "checkpoint-delta":
		return store.CommitCheckpointTurnWithAccountingBatchVersioned(ctx, f.record, f.update, f.expected, accounting, history...)
	default:
		return store.CommitBeforeTurnWithAccountingBatchVersioned(ctx, f.record, f.update, f.boundary, "ignored-selection", f.expected, accounting, history...)
	}
}

func auxiliaryBatchEvidence() db.SessionAccountingBatch {
	return db.SessionAccountingBatch{
		Roots:       []db.SessionProviderAccounting{accountingEnvelope("owner")},
		Children:    []db.SessionChildProviderAccounting{childAccountingEnvelope("owner")},
		Auxiliaries: []db.SessionAuxiliaryProviderAccounting{auxiliaryAccountingEnvelope("owner")},
	}
}

func assertAuxiliarySetsEmpty(t *testing.T, store *db.Store) {
	t.Helper()
	if childAccountingCount(t, store) != 0 {
		t.Fatal("child evidence escaped rollback")
	}
	rows, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "source", 0, 256)
	if err != nil || len(rows) != 0 {
		t.Fatalf("auxiliary evidence escaped rollback: %v", err)
	}
}

func TestAuxiliaryBatchAtomicFailureRetryAndSameTransactionVersion(t *testing.T) {
	t.Parallel()
	for _, mode := range auxiliaryBatchModes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newAuxiliaryBatchFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			evidence := auxiliaryBatchEvidence()
			history := versionedAccountingBatch("owner")
			changed := evidence.Auxiliaries[0].Clone()
			changed.PayloadJSON = []byte(`{"changed":true}`)
			conflict := auxiliaryBatchEvidence()
			conflict.Auxiliaries = append(conflict.Auxiliaries, changed)
			missing := auxiliaryBatchEvidence()
			missing.Auxiliaries[0].OwnerRequestBatchID = "missing-owner"
			for _, tc := range []struct {
				name     string
				evidence db.SessionAccountingBatch
				want     error
			}{
				{"late auxiliary conflict", conflict, db.ErrAuxiliaryProviderAccountingConflict},
				{"missing auxiliary owner", missing, db.ErrAuxiliaryProviderAccountingInvalidRequest},
			} {
				t.Run(tc.name, func(t *testing.T) {
					version, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, tc.evidence, history)
					if !errors.Is(err, tc.want) || version != (db.SessionContentVersion{}) {
						t.Fatalf("commit error/version: %v", err)
					}
					assertVersionedAccountingUnchanged(t, f, before)
					assertAuxiliarySetsEmpty(t, f.store)
				})
			}
			// Both an insertion failure and a deferred COMMIT failure occur
			// after all preceding state, history, root and child writes.
			for _, trigger := range []string{
				`CREATE TRIGGER reject_aux BEFORE INSERT ON session_auxiliary_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected'); END`,
				`CREATE TABLE deferred_aux_guard (id TEXT REFERENCES sessions(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER reject_aux AFTER INSERT ON session_auxiliary_provider_accounting BEGIN INSERT INTO deferred_aux_guard VALUES ('missing-session'); END`,
			} {
				if _, err := f.store.DB().ExecContext(t.Context(), trigger); err != nil {
					t.Fatal(err)
				}
				version, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, evidence, history)
				if err == nil || version != (db.SessionContentVersion{}) {
					t.Fatalf("injected failure: %v", err)
				}
				assertVersionedAccountingUnchanged(t, f, before)
				assertAuxiliarySetsEmpty(t, f.store)
				if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_aux`); err != nil {
					t.Fatal(err)
				}
			}
			version, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, evidence, history)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(mode, "unversioned/") && (version == (db.SessionContentVersion{}) || version != observed || version == f.expected) {
				t.Fatal("returned version is not the committed row version")
			}
			root, err := f.store.GetSessionProviderAccounting(t.Context(), f.record.ID, "owner")
			if err != nil || !reflect.DeepEqual(root.Accounting, evidence.Roots[0]) {
				t.Fatal("root not atomic", err)
			}
			child, err := f.store.GetSessionChildProviderAccounting(t.Context(), f.record.ID, "owner", 1)
			if err != nil || !reflect.DeepEqual(child.Accounting, evidence.Children[0]) {
				t.Fatal("child not atomic", err)
			}
			aux, err := f.store.GetSessionAuxiliaryProviderAccounting(t.Context(), f.record.ID, "owner")
			if err != nil || !reflect.DeepEqual(aux.Accounting, evidence.Auxiliaries[0]) {
				t.Fatal("aux not atomic", err)
			}
			if strings.HasPrefix(mode, "before-") {
				checkpoint, err := f.store.GetSessionCheckpoint(t.Context(), f.record.ID, f.boundary.Checkpoint.ID)
				if err != nil {
					t.Fatal(err)
				}
				entries, messages, state, err := f.store.ReadCheckpointHistory(t.Context(), checkpoint.History)
				if err != nil || len(entries) != len(f.boundary.Entries) || len(messages) != 1 || string(state) != string(f.boundary.State) {
					t.Fatal("BEFORE history", err)
				}
			}
			f.expected = observed
			f.update.ExpectedRevision, f.update.Entries = f.update.Revision, nil
			f.boundary.Checkpoint.ID = "retry-before"
			evidence.Auxiliaries = append(evidence.Auxiliaries, evidence.Auxiliaries[0])
			if _, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, evidence, history); err != nil {
				t.Fatal("exact retry", err)
			}
			again, err := f.store.GetSessionAuxiliaryProviderAccounting(t.Context(), f.record.ID, "owner")
			if err != nil || !reflect.DeepEqual(aux, again) {
				t.Fatal("retry changed cursor/bytes", err)
			}
			model, err := f.store.GetSessionModelContext(t.Context(), f.record.ID)
			if err != nil || model.Generation != 1 {
				t.Fatal("history duplicated", err)
			}
		})
	}
}

func TestAuxiliaryBatchInputAndExistingGuards(t *testing.T) {
	t.Parallel()
	for _, mode := range auxiliaryBatchModes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newAuxiliaryBatchFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			evidence := auxiliaryBatchEvidence()
			invalid := auxiliaryBatchEvidence()
			invalid.Auxiliaries[0].Version = 2
			if v, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, invalid); !errors.Is(err, db.ErrAuxiliaryProviderAccountingVersion) || v != (db.SessionContentVersion{}) {
				t.Fatal("version", err)
			}
			// Root/child validation order is unchanged, even with invalid auxiliaries.
			invalid.Roots[0].Version = 2
			if _, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, invalid); !errors.Is(err, db.ErrProviderAccountingVersion) {
				t.Fatal("root order", err)
			}
			invalid.Roots = nil
			invalid.Children[0].Version = 2
			if _, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, invalid); !errors.Is(err, db.ErrChildProviderAccountingVersion) {
				t.Fatal("child order", err)
			}
			wrong := f
			wrong.record.ID = "another-session"
			onlyAux := db.SessionAccountingBatch{Auxiliaries: evidence.Auxiliaries}
			if v, err := commitAuxiliaryBatch(t.Context(), wrong, mode, f.store, onlyAux); !errors.Is(err, db.ErrAuxiliaryProviderAccountingInvalidRequest) || v != (db.SessionContentVersion{}) {
				t.Fatal("scope", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if v, err := commitAuxiliaryBatch(ctx, f, mode, f.store, evidence); !errors.Is(err, context.Canceled) || v != (db.SessionContentVersion{}) {
				t.Fatal("cancellation", err)
			}
			if strings.HasSuffix(f.mode, "-delta") || f.mode == "checkpoint" || f.mode == "before-existing" {
				wrong = f
				wrong.update.ExpectedRevision++
				if v, err := commitAuxiliaryBatch(t.Context(), wrong, mode, f.store, evidence, versionedAccountingBatch("owner")); err == nil || v != (db.SessionContentVersion{}) {
					t.Fatal("revision/source accepted", err)
				}
			}
			if strings.HasPrefix(f.mode, "before-") {
				wrong = f
				wrong.boundary.Checkpoint.SourceSessionID = "another-session"
				if v, err := commitAuxiliaryBatch(t.Context(), wrong, mode, f.store, evidence, versionedAccountingBatch("owner")); !errors.Is(err, db.ErrCheckpointCorrupt) || v != (db.SessionContentVersion{}) {
					t.Fatal("BEFORE source", err)
				}
				if f.mode == "before-delta" {
					wrong = f
					wrong.boundary.Entries = nil
					if v, err := commitAuxiliaryBatch(t.Context(), wrong, mode, f.store, evidence, versionedAccountingBatch("owner")); !errors.Is(err, db.ErrTranscriptConflict) || v != (db.SessionContentVersion{}) {
						t.Fatal("BEFORE immutable prefix", err)
					}
				}
			}
			assertVersionedAccountingUnchanged(t, f, before)
			assertAuxiliarySetsEmpty(t, f.store)
			// Nil auxiliary compatibility with the existing root/child path.
			evidence.Auxiliaries = nil
			if _, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, evidence, versionedAccountingBatch("owner")); err != nil {
				t.Fatal("nil auxiliaries", err)
			}
			observed, err := f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.SaveSessionDraftVersioned(t.Context(), f.record, observed); err != nil {
				t.Fatal(err)
			}
			// A visibly changed draft makes the original expected version stale.
			changed := f.record
			changed.PendingJSON = []byte(`{"foreign":true}`)
			observed, err = f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.SaveSessionDraftVersioned(t.Context(), changed, observed); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(mode, "unversioned/completed") {
				before = snapshotVersionedAccounting(t, f)
				if v, err := commitAuxiliaryBatch(t.Context(), f, mode, f.store, onlyAux); !errors.Is(err, db.ErrCheckpointConflict) || v != (db.SessionContentVersion{}) {
					t.Fatal("CAS", err)
				}
				assertVersionedAccountingUnchanged(t, f, before)
			}
		})
	}
}

func TestAuxiliaryBatchNonversionedBorrowedTransactions(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"unversioned/completed-delta", "unversioned/checkpoint-delta"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newAuxiliaryBatchFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			rollback := errors.New("caller rollback")
			if err := f.store.WithTx(t.Context(), func(tx *db.Store) error {
				if _, err := commitAuxiliaryBatch(t.Context(), f, mode, tx, auxiliaryBatchEvidence(), versionedAccountingBatch("owner")); err != nil {
					return err
				}
				if _, err := tx.GetSessionAuxiliaryProviderAccounting(t.Context(), f.record.ID, "owner"); err != nil {
					return err
				}
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			assertAuxiliarySetsEmpty(t, f.store)
			if err := f.store.WithTx(t.Context(), func(tx *db.Store) error {
				_, err := commitAuxiliaryBatch(t.Context(), f, mode, tx, auxiliaryBatchEvidence(), versionedAccountingBatch("owner"))
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
