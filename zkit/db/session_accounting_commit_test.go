package db_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func commitChildAccounting(t *testing.T, f versionedAccountingFixture, roots []db.SessionProviderAccounting, children []db.SessionChildProviderAccounting, history ...db.SessionHistoryBatch) (db.SessionContentVersion, error) {
	t.Helper()
	switch f.mode {
	case "completed", "completed-delta":
		return f.store.CommitCompletedTurnWithAccountingVersioned(t.Context(), f.record, f.update, f.expected, roots, children, history...)
	case "checkpoint", "checkpoint-delta":
		return f.store.CommitCheckpointTurnWithAccountingVersioned(t.Context(), f.record, f.update, f.expected, roots, children, history...)
	default:
		return f.store.CommitBeforeTurnWithAccountingVersioned(t.Context(), f.record, f.update, f.boundary, "ignored-selection", f.expected, roots, children, history...)
	}
}

func childAccountingCount(t *testing.T, store *db.Store) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT count(*) FROM session_child_provider_accounting").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestChildAccountingCombinedAtomicCommits(t *testing.T) {
	t.Parallel()
	for _, mode := range versionedAccountingModes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			root := accountingEnvelope("owner")
			child := childAccountingEnvelope("owner")
			child.ChildTaskID = "spawn-uuid"
			changed := child.Clone()
			changed.PayloadJSON = []byte(`{"different":true}`)
			batch := versionedAccountingBatch("owner")
			for _, tc := range []struct {
				name     string
				children []db.SessionChildProviderAccounting
				history  []db.SessionHistoryBatch
				want     error
			}{
				{"late duplicate conflict", []db.SessionChildProviderAccounting{child, changed}, []db.SessionHistoryBatch{batch}, db.ErrChildProviderAccountingConflict},
				{"missing owner", []db.SessionChildProviderAccounting{child}, nil, db.ErrProviderAccountingInvalidRequest},
			} {
				t.Run(tc.name, func(t *testing.T) {
					version, err := commitChildAccounting(t, f, []db.SessionProviderAccounting{root}, tc.children, tc.history...)
					if !errors.Is(err, tc.want) || version != (db.SessionContentVersion{}) {
						t.Fatalf("failed commit: %v", err)
					}
					assertVersionedAccountingUnchanged(t, f, before)
					if childAccountingCount(t, f.store) != 0 {
						t.Fatal("child escaped rollback")
					}
				})
			}
			// Inject failure after session/history/root/checkpoint writes, at the
			// final child insertion; none of those preceding writes may escape.
			if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_child BEFORE INSERT ON session_child_provider_accounting BEGIN SELECT RAISE(ABORT, 'injected child failure'); END`); err != nil {
				t.Fatal(err)
			}
			if version, err := commitChildAccounting(t, f, []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child}, batch); err == nil || version != (db.SessionContentVersion{}) {
				t.Fatalf("injected failure: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			if childAccountingCount(t, f.store) != 0 {
				t.Fatal("child escaped failed insert")
			}
			if _, err := f.store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_child`); err != nil {
				t.Fatal(err)
			}
			version, err := commitChildAccounting(t, f, []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child}, batch)
			if err != nil || version == (db.SessionContentVersion{}) || version == f.expected {
				t.Fatalf("retry commit: %v", err)
			}
			state, err := f.store.GetSessionResumeState(t.Context(), f.record.ID)
			if err != nil || state.ContentVersion != version || state.Transcript == nil || state.Transcript.Revision != f.update.Revision ||
				!reflect.DeepEqual(state.Session.ContextJSON, f.record.ContextJSON) || !reflect.DeepEqual(state.Session.LastUsageJSON, f.record.LastUsageJSON) {
				t.Fatalf("same transaction receipt/state: %v", err)
			}
			got, err := f.store.GetSessionChildProviderAccounting(t.Context(), f.record.ID, child.ChildTaskID, child.Attempt)
			if err != nil || !reflect.DeepEqual(got.Accounting, child) {
				t.Fatalf("child: %v", err)
			}
			rootGot, err := f.store.GetSessionProviderAccounting(t.Context(), f.record.ID, root.RequestBatchID)
			if err != nil || !reflect.DeepEqual(rootGot.Accounting, root) {
				t.Fatalf("root: %v", err)
			}
			if mode == "before-initial" || mode == "before-existing" || mode == "before-delta" {
				if _, err := f.store.GetSessionCheckpoint(t.Context(), f.record.ID, f.boundary.Checkpoint.ID); err != nil {
					t.Fatal(err)
				}
			}
			f.expected = version
			f.update.ExpectedRevision, f.update.Entries = f.update.Revision, nil
			f.boundary.Checkpoint.ID = "retry-before"
			if _, err := commitChildAccounting(t, f, []db.SessionProviderAccounting{root}, []db.SessionChildProviderAccounting{child, child}, batch); err != nil {
				t.Fatalf("identical retry: %v", err)
			}
			again, err := f.store.GetSessionChildProviderAccounting(t.Context(), f.record.ID, child.ChildTaskID, child.Attempt)
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatalf("retry changed child cursor: %v", err)
			}
			model, err := f.store.GetSessionModelContext(t.Context(), f.record.ID)
			if err != nil || model.Generation != 1 {
				t.Fatalf("history duplicated: %v", err)
			}
			// Later independent writes cannot be adopted as this commit's receipt.
			other, err := db.Open(t.Context(), f.path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			observed, err := f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			competing := f.record
			competing.PendingJSON = []byte(`{"foreign":true}`)
			if _, err := other.SaveSessionDraftVersioned(t.Context(), competing, observed); err != nil {
				t.Fatal(err)
			}
			f.expected = version
			before = snapshotVersionedAccounting(t, f)
			if v, err := commitChildAccounting(t, f, nil, []db.SessionChildProviderAccounting{child}); !errors.Is(err, db.ErrCheckpointConflict) || v != (db.SessionContentVersion{}) {
				t.Fatalf("stale CAS: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
		})
	}
}

func TestChildAccountingCombinedInputAndSourceGuards(t *testing.T) {
	t.Parallel()
	for _, mode := range versionedAccountingModes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			child := childAccountingEnvelope("owner")
			child.Version = 2
			if version, err := commitChildAccounting(t, f, nil, []db.SessionChildProviderAccounting{child}); !errors.Is(err, db.ErrChildProviderAccountingVersion) || version != (db.SessionContentVersion{}) {
				t.Fatalf("invalid version: %v", err)
			}
			child.Version, child.Attempt = 1, 0
			if version, err := commitChildAccounting(t, f, nil, []db.SessionChildProviderAccounting{child}); !errors.Is(err, db.ErrChildProviderAccountingInvalidRequest) || version != (db.SessionContentVersion{}) {
				t.Fatalf("invalid attempt: %v", err)
			}
			child.Attempt = 1
			// The owner need not have root settlement, but it must have a receipt.
			if version, err := commitChildAccounting(t, f, nil, []db.SessionChildProviderAccounting{child}); !errors.Is(err, db.ErrChildProviderAccountingInvalidRequest) || version != (db.SessionContentVersion{}) {
				t.Fatalf("missing owner: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			cross := f
			cross.update.SessionID = "another-session"
			if version, err := commitChildAccounting(t, cross, nil, []db.SessionChildProviderAccounting{child}); !errors.Is(err, db.ErrChildProviderAccountingInvalidRequest) || version != (db.SessionContentVersion{}) {
				t.Fatalf("cross session operation: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			// Nil children keep the exact old root-only operation usable.
			if version, err := commitChildAccounting(t, f, []db.SessionProviderAccounting{accountingEnvelope("root")}, nil, versionedAccountingBatch("root")); err != nil || version == (db.SessionContentVersion{}) {
				t.Fatalf("nil child compatibility: %v", err)
			}
			if childAccountingCount(t, f.store) != 0 {
				t.Fatal("nil children invented evidence")
			}
		})
	}
	for _, mode := range []string{"checkpoint", "checkpoint-delta", "before-existing", "before-delta"} {
		t.Run(mode+"/source", func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			// Observe the current version but deliberately supply a stale revision.
			f.update.ExpectedRevision = 2
			before := snapshotVersionedAccounting(t, f)
			if version, err := commitChildAccounting(t, f, nil, []db.SessionChildProviderAccounting{childAccountingEnvelope("owner")}, versionedAccountingBatch("owner")); err == nil || version != (db.SessionContentVersion{}) {
				t.Fatalf("source guard: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			if childAccountingCount(t, f.store) != 0 {
				t.Fatal("source conflict stored child")
			}
		})
	}
}

func TestChildAccountingOwnerConflictAndPageIntegrity(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	for _, owner := range []string{"owner", "other-owner"} {
		saveAccountingReceipt(t, store, "source", owner)
	}
	entry := childAccountingEnvelope("owner")
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{entry}); err != nil {
		t.Fatal(err)
	}
	changed := entry.Clone()
	changed.OwnerRequestBatchID = "other-owner"
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{changed}); !errors.Is(err, db.ErrChildProviderAccountingConflict) {
		t.Fatalf("changed owner: %v", err)
	}
	changed.Version = 2
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{changed}); !errors.Is(err, db.ErrChildProviderAccountingVersion) {
		t.Fatalf("changed version: %v", err)
	}
	second := entry.Clone()
	second.Attempt++
	if err := store.SaveSessionChildProviderAccounting(t.Context(), "source", []db.SessionChildProviderAccounting{second}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_child_provider_accounting SET checksum = 'PRIVATE-CANARY' WHERE attempt = 2"); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListSessionChildProviderAccounting(t.Context(), "source", 0, 256)
	if !errors.Is(err, db.ErrChildProviderAccountingCorrupt) || page != nil || errors.Unwrap(err) != nil {
		t.Fatalf("corrupt second record returned partial page: %v", err)
	}
}
