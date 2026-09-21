package db_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestBeforeVersionReceiptAndRollback(t *testing.T) {
	store, checkpoint := historyStore(t)
	record, err := store.GetSession(t.Context(), checkpoint.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.SessionVersion(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.ID = "versioned-before"
	boundary := preparedHistoryBoundary(checkpoint, nil)
	update := db.TranscriptUpdate{SessionID: record.ID, Workspace: record.Workspace}
	record.PendingJSON = []byte(`{"text":"protected"}`)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_receipt BEFORE INSERT ON session_checkpoints BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	rejected, err := store.CommitBeforeTurnVersioned(t.Context(), record, update, boundary, "", initial)
	if err == nil || rejected != (db.SessionContentVersion{}) {
		t.Fatal("failed BEFORE published receipt")
	}
	unchanged, err := store.SessionVersion(t.Context(), record.ID)
	if err != nil || unchanged != initial {
		t.Fatalf("failed BEFORE changed row: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DROP TRIGGER reject_receipt`); err != nil {
		t.Fatal(err)
	}
	committed, err := store.CommitBeforeTurnVersioned(t.Context(), record, update, boundary, "", initial)
	if err != nil || committed == initial {
		t.Fatalf("BEFORE receipt = unchanged: %v", err)
	}
	loaded, err := store.GetSessionResumeState(t.Context(), record.ID)
	if err != nil || loaded.ContentVersion != committed {
		t.Fatalf("load not paired with BEFORE receipt: %v", err)
	}
	if err := store.RenameSession(t.Context(), record.ID, "foreign"); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSessionResumeState(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	boundary.Checkpoint.ID = "next-before"
	rejected, err = store.CommitBeforeTurnVersioned(t.Context(), record, update, boundary, "", committed)
	if !errors.Is(err, db.ErrCheckpointConflict) || rejected != (db.SessionContentVersion{}) {
		t.Fatalf("stale BEFORE = %v", err)
	}
	after, err := store.GetSessionResumeState(t.Context(), record.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("stale BEFORE changed foreign row: %v", err)
	}
}

func TestDeleteSessionVersionedPreservesCompetingRow(t *testing.T) {
	store, checkpoint := historyStore(t)
	id := checkpoint.SessionID
	initial, err := store.SessionVersion(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RenameSession(t.Context(), id, "foreign"); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSessionVersioned(t.Context(), id, initial); !errors.Is(err, db.ErrCheckpointConflict) {
		t.Fatalf("stale delete = %v", err)
	}
	after, err := store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("stale delete changed foreign row: %v", err)
	}
	if err := store.DeleteSessionVersioned(t.Context(), id, before.ContentVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(t.Context(), id); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("matching delete = %v", err)
	}
}

func TestCheckpointBranchVersionedRejectsWriterAfterPreservation(t *testing.T) {
	store, checkpoint := checkpointStore(t)
	checkpoint = saveCheckpoint(t, store, checkpoint)
	loaded, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Session.PendingJSON = []byte(`{"text":"local preserved draft"}`)
	preserved, err := store.SaveCheckpointSourceVersioned(t.Context(), loaded.Session, checkpoint.SourceRevision, loaded.ContentVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RenameSession(t.Context(), checkpoint.SessionID, "foreign rename after preservation"); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	branch := emptyCheckpointBranch(checkpoint)
	version, err := store.CreateCheckpointBranchVersioned(t.Context(), branch, preserved)
	if !errors.Is(err, db.ErrCheckpointConflict) || version != (db.SessionContentVersion{}) {
		t.Fatalf("stale branch publication = %v", err)
	}
	if _, err := store.GetSession(t.Context(), branch.Child.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("stale publication created child: %v", err)
	}
	if _, err := store.GetSessionBranch(t.Context(), branch.Child.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("stale publication created provenance: %v", err)
	}
	active, err := store.GetSettingExact(t.Context(), checkpoint.Workspace, "active_session")
	if err != nil || active != checkpoint.SessionID {
		t.Fatalf("stale publication changed active selection: %v", err)
	}
	after, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("stale publication changed source: %v", err)
	}
	version, err = store.CreateCheckpointBranchVersioned(t.Context(), branch, before.ContentVersion)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.GetSessionResumeState(t.Context(), branch.Child.ID)
	if err != nil || child.ContentVersion != version {
		t.Fatalf("child publication receipt does not match loaded snapshot: %v", err)
	}
}
