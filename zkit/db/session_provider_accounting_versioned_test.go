package db_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

var versionedAccountingModes = []string{"completed", "completed-delta", "checkpoint", "checkpoint-delta", "before-initial", "before-existing", "before-delta"}

type versionedAccountingFixture struct {
	store    *db.Store
	path     string
	mode     string
	record   db.SessionRecord
	update   db.TranscriptUpdate
	boundary db.HistoryCheckpoint
	expected db.SessionContentVersion
}

func newVersionedAccountingFixture(t *testing.T, mode string) versionedAccountingFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "versioned.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if mode != "before-initial" {
		initializeAccountingSession(t, store, "source", "/workspace")
	}
	record := db.SessionRecord{ID: "source", Workspace: "/workspace", ContextJSON: []byte(`[{"text":"settled"}]`), LastUsageJSON: []byte(`{"CostUSD":0.75}`)}
	update := db.TranscriptUpdate{SessionID: record.ID, Workspace: record.Workspace}
	switch mode {
	case "completed-delta", "checkpoint-delta", "before-delta":
		update.Revision = 1
		update.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "answer", Revision: 1, Kind: "assistant_message", PayloadJSON: []byte(`{"text":"settled"}`)}}
	}
	boundary := preparedHistoryBoundary(db.SessionCheckpoint{
		ID: "accounting-before", SessionID: record.ID, SourceSessionID: record.ID,
		Workspace: record.Workspace, FormatVersion: 2, BoundaryID: "prompt", SourceRevision: update.Revision,
	}, update.Entries)
	expected, err := store.SessionVersion(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	return versionedAccountingFixture{store: store, path: path, mode: mode, record: record, update: update, boundary: boundary, expected: expected}
}

func (f versionedAccountingFixture) commit(t *testing.T, accounting []db.SessionProviderAccounting, history ...db.SessionHistoryBatch) (db.SessionContentVersion, error) {
	t.Helper()
	switch f.mode {
	case "completed", "completed-delta":
		return f.store.CommitCompletedTurnWithProviderAccountingVersioned(t.Context(), f.record, f.update, f.expected, accounting, history...)
	case "checkpoint", "checkpoint-delta":
		return f.store.CommitCheckpointTurnWithProviderAccountingVersioned(t.Context(), f.record, f.update, f.expected, accounting, history...)
	default:
		return f.store.CommitBeforeTurnWithProviderAccountingVersioned(t.Context(), f.record, f.update, f.boundary, "ignored-selection", f.expected, accounting, history...)
	}
}

func versionedAccountingBatch(id string) db.SessionHistoryBatch {
	return db.SessionHistoryBatch{ID: id, Messages: [][]byte{[]byte(`{"text":"retained"}`)}, RequestJSON: []byte(`{"prepared":true}`)}
}

func TestProviderAccountingVersionedReceipts(t *testing.T) {
	t.Parallel()
	for _, mode := range versionedAccountingModes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			entry := accountingEnvelope("request")
			version, err := f.commit(t, []db.SessionProviderAccounting{entry}, versionedAccountingBatch(entry.RequestBatchID))
			if err != nil || version == (db.SessionContentVersion{}) || version == f.expected {
				t.Fatalf("commit receipt: %v", err)
			}
			state, err := f.store.GetSessionResumeState(t.Context(), f.record.ID)
			if err != nil || state.ContentVersion != version || state.Transcript == nil || state.Transcript.Revision != f.update.Revision ||
				string(state.Session.ContextJSON) != string(f.record.ContextJSON) || string(state.Session.LastUsageJSON) != string(f.record.LastUsageJSON) {
				t.Fatalf("receipt does not describe committed session/transcript: %v", err)
			}
			settlement, err := f.store.GetSessionProviderAccounting(t.Context(), f.record.ID, entry.RequestBatchID)
			if err != nil || !reflect.DeepEqual(settlement.Accounting, entry) {
				t.Fatalf("settlement unavailable with same-transaction history receipt: %v", err)
			}
			model, err := f.store.GetSessionModelContext(t.Context(), f.record.ID)
			if err != nil || model.Generation != 1 {
				t.Fatalf("request receipt: %v", err)
			}
			if mode != "completed" && mode != "completed-delta" && mode != "checkpoint" && mode != "checkpoint-delta" {
				checkpoint, err := f.store.GetSessionCheckpoint(t.Context(), f.record.ID, f.boundary.Checkpoint.ID)
				if err != nil {
					t.Fatal(err)
				}
				entries, messages, state, err := f.store.ReadCheckpointHistory(t.Context(), checkpoint.History)
				if err != nil || len(entries) != len(f.boundary.Entries) || len(messages) != 1 || string(messages[0]) != `{"text":"retained"}` || string(state) != string(f.boundary.State) {
					t.Fatalf("BEFORE checkpoint history: %v", err)
				}
				for i, entry := range entries {
					if !reflect.DeepEqual(entry, f.boundary.Entries[i]) {
						t.Fatalf("checkpoint entry %d differs from committed boundary", i)
					}
				}
			}
			// A later independent write must not become authority carried by this
			// local receipt. It stays tied to the row this operation committed.
			other, err := db.Open(t.Context(), f.path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			competing := state.Session
			competing.PendingJSON = []byte(`{"text":"independent writer"}`)
			foreign, err := other.SaveSessionDraftVersioned(t.Context(), competing, version)
			if err != nil || foreign == version {
				t.Fatalf("independent draft: %v", err)
			}
			got, err := f.store.SaveSessionDraftVersioned(t.Context(), f.record, version)
			if !errors.Is(err, db.ErrCheckpointConflict) || got != (db.SessionContentVersion{}) {
				t.Fatalf("local receipt adopted competing write: %v", err)
			}
		})
	}
}

// Observe the public resume snapshot and dependent-table sizes, so late failures
// cannot leave a checkpoint, receipt, immutable prefix, or active selection behind.
type versionedAccountingSnapshot struct {
	resume db.SessionResumeState
	err    error
	active string
	counts []int
}

func snapshotVersionedAccounting(t *testing.T, f versionedAccountingFixture) versionedAccountingSnapshot {
	t.Helper()
	resume, err := f.store.GetSessionResumeState(t.Context(), f.record.ID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		t.Fatal(err)
	}
	active, activeErr := f.store.GetSettingExact(t.Context(), f.record.Workspace, "active_session")
	if activeErr != nil && !errors.Is(activeErr, db.ErrNotFound) {
		t.Fatal(activeErr)
	}
	snapshot := versionedAccountingSnapshot{resume: resume, err: err, active: active}
	for _, table := range []string{"sessions", "session_transcripts", "session_transcript_entries", "session_history_batches", "session_provider_accounting", "session_checkpoints", "session_checkpoint_history", "session_history_nodes", "session_history_values", "session_history_heads", "session_model_context"} {
		var count int
		if err := f.store.DB().QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		snapshot.counts = append(snapshot.counts, count)
	}
	return snapshot
}

func assertVersionedAccountingUnchanged(t *testing.T, f versionedAccountingFixture, before versionedAccountingSnapshot) {
	t.Helper()
	after := snapshotVersionedAccounting(t, f)
	if !errors.Is(after.err, before.err) || !reflect.DeepEqual(before.resume, after.resume) || before.active != after.active || !reflect.DeepEqual(before.counts, after.counts) {
		t.Fatal("failed write changed session, transcript, history, checkpoint, accounting, or selection")
	}
}

func TestProviderAccountingVersionedRollbackAndRetry(t *testing.T) {
	t.Parallel()
	for _, mode := range versionedAccountingModes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			entry := accountingEnvelope("request")
			batch := versionedAccountingBatch(entry.RequestBatchID)
			// Two different envelopes for the same newly inserted receipt conflict
			// after history and the first accounting insert, even for initial BEFORE.
			changed := entry.Clone()
			changed.PayloadJSON = []byte(`{"different":true}`)
			version, err := f.commit(t, []db.SessionProviderAccounting{entry, changed}, batch)
			if !errors.Is(err, db.ErrProviderAccountingConflict) || version != (db.SessionContentVersion{}) {
				t.Fatalf("accounting conflict receipt: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			version, err = f.commit(t, []db.SessionProviderAccounting{entry})
			if !errors.Is(err, db.ErrProviderAccountingInvalidRequest) || version != (db.SessionContentVersion{}) {
				t.Fatalf("missing request receipt: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			// Retrying precisely the failed snapshot now commits all components.
			version, err = f.commit(t, []db.SessionProviderAccounting{entry}, batch)
			if err != nil || version == (db.SessionContentVersion{}) {
				t.Fatalf("retry: %v", err)
			}
			settlement, err := f.store.GetSessionProviderAccounting(t.Context(), f.record.ID, entry.RequestBatchID)
			if err != nil {
				t.Fatal(err)
			}
			f.expected = version
			f.update.ExpectedRevision, f.update.Entries = f.update.Revision, nil
			if mode == "completed" || mode == "completed-delta" || mode == "checkpoint" || mode == "checkpoint-delta" {
				if _, err := f.commit(t, []db.SessionProviderAccounting{entry}, batch); err != nil {
					t.Fatalf("idempotent terminal retry: %v", err)
				}
			} else {
				before = snapshotVersionedAccounting(t, f)
				if got, err := f.commit(t, []db.SessionProviderAccounting{entry}, batch); !errors.Is(err, db.ErrCheckpointConflict) || got != (db.SessionContentVersion{}) {
					t.Fatalf("BEFORE identity reuse allowed dispatch: %v", err)
				}
				assertVersionedAccountingUnchanged(t, f, before)
				f.boundary.Checkpoint.ID = "next-before"
				if _, err := f.commit(t, []db.SessionProviderAccounting{entry}, batch); err != nil {
					t.Fatalf("settlement retry with fresh BEFORE boundary: %v", err)
				}
			}
			again, err := f.store.GetSessionProviderAccounting(t.Context(), f.record.ID, entry.RequestBatchID)
			if err != nil || !reflect.DeepEqual(settlement, again) {
				t.Fatalf("retry changed settlement cursor/content: %v", err)
			}
			model, err := f.store.GetSessionModelContext(t.Context(), f.record.ID)
			if err != nil || model.Generation != 1 {
				t.Fatalf("retry duplicated history receipt: %v", err)
			}
			// Conflict against an already durable settlement must also roll back
			// a changed session and an additional history batch/checkpoint.
			f.expected, err = f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			before = snapshotVersionedAccounting(t, f)
			f.record.ContextJSON = []byte(`[{"text":"must roll back"}]`)
			f.boundary.Checkpoint.ID = "conflicting-before"
			if got, err := f.commit(t, []db.SessionProviderAccounting{changed}, versionedAccountingBatch("must-rollback")); !errors.Is(err, db.ErrProviderAccountingConflict) || got != (db.SessionContentVersion{}) {
				t.Fatalf("durable accounting conflict: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
		})
	}
}

func TestProviderAccountingVersionedStaleAndInvalid(t *testing.T) {
	t.Parallel()
	for _, mode := range versionedAccountingModes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			before := snapshotVersionedAccounting(t, f)
			entry := accountingEnvelope("request")
			entry.Version = 2
			if version, err := f.commit(t, []db.SessionProviderAccounting{entry}); !errors.Is(err, db.ErrProviderAccountingVersion) || version != (db.SessionContentVersion{}) {
				t.Fatalf("unsupported envelope: %v", err)
			}
			entry.Version, entry.PayloadJSON = 1, []byte(`not json`)
			if version, err := f.commit(t, []db.SessionProviderAccounting{entry}); !errors.Is(err, db.ErrProviderAccountingInvalidRequest) || version != (db.SessionContentVersion{}) {
				t.Fatalf("invalid envelope: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
			other, err := db.Open(t.Context(), f.path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			competing := f.record
			competing.PendingJSON = []byte(`{"text":"competing"}`)
			if _, err := other.SaveSessionDraftVersioned(t.Context(), competing, f.expected); err != nil {
				t.Fatal(err)
			}
			before = snapshotVersionedAccounting(t, f)
			entry = accountingEnvelope("request")
			if version, err := f.commit(t, []db.SessionProviderAccounting{entry}, versionedAccountingBatch(entry.RequestBatchID)); !errors.Is(err, db.ErrCheckpointConflict) || version != (db.SessionContentVersion{}) {
				t.Fatalf("stale source receipt: %v", err)
			}
			assertVersionedAccountingUnchanged(t, f, before)
		})
	}
}

func TestProviderAccountingVersionedNilAccounting(t *testing.T) {
	t.Parallel()
	for _, mode := range versionedAccountingModes {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newVersionedAccountingFixture(t, mode)
			version, err := f.commit(t, nil, versionedAccountingBatch("unsettled"))
			if err != nil || version == (db.SessionContentVersion{}) {
				t.Fatalf("nil accounting receipt: %v", err)
			}
			observed, err := f.store.SessionVersion(t.Context(), f.record.ID)
			if err != nil || observed != version {
				t.Fatalf("nil accounting changed receipt behavior: %v", err)
			}
			rows, err := f.store.ListSessionProviderAccounting(t.Context(), f.record.ID, 0, 256)
			if err != nil || len(rows) != 0 {
				t.Fatalf("unsettled request fabricated accounting: %v", err)
			}
		})
	}
}

func TestProviderAccountingBeforeVersionedRetainsGuards(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"before-initial", "before-existing", "before-delta"} {
		for _, guard := range []string{"quota", "metadata", "state", "revision", "prefix"} {
			t.Run(mode+"/"+guard, func(t *testing.T) {
				t.Parallel()
				f := newVersionedAccountingFixture(t, mode)
				before := snapshotVersionedAccounting(t, f)
				want := db.ErrCheckpointCorrupt
				switch guard {
				case "quota":
					f.boundary.Checkpoint.Payload = make([]byte, db.CheckpointPayloadLimit+1)
					want = db.ErrCheckpointQuota
				case "metadata":
					f.boundary.Checkpoint.SourceSessionID = "foreign"
				case "state":
					f.boundary.State = []byte(`{"wrong":"state"}`)
				case "revision":
					f.update.ExpectedRevision = 2
					want = db.ErrTranscriptConflict
					if mode == "before-initial" {
						want = db.ErrNotFound
					}
				case "prefix":
					f.boundary.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "invented", Revision: 1, Kind: "assistant_message", PayloadJSON: []byte(`{}`)}}
					want = db.ErrTranscriptConflict
					if mode == "before-initial" {
						want = db.ErrNotFound
					}
				}
				entry := accountingEnvelope("guarded")
				version, err := f.commit(t, []db.SessionProviderAccounting{entry}, versionedAccountingBatch(entry.RequestBatchID))
				if !errors.Is(err, want) || version != (db.SessionContentVersion{}) {
					t.Fatalf("BEFORE %s guard: got %v, want %v", guard, err, want)
				}
				assertVersionedAccountingUnchanged(t, f, before)
			})
		}
	}
}
