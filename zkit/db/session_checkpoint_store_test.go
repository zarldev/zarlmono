package db_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

func checkpointStore(t *testing.T) (*db.Store, db.SessionCheckpoint) {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveActiveSession(t.Context(), db.SessionRecord{ID: "source", Workspace: "/workspace"}); err != nil {
		t.Fatal(err)
	}
	return store, db.SessionCheckpoint{
		SessionID: "source", SourceSessionID: "source", ID: "before-first", Workspace: "/workspace",
		BoundaryID: "prompt-first", FormatVersion: 1, Payload: []byte(`{"context":[],"records":[]}`),
	}
}

func saveCheckpoint(t *testing.T, store *db.Store, checkpoint db.SessionCheckpoint) db.SessionCheckpoint {
	t.Helper()
	if err := store.SaveSessionCheckpoint(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSessionCheckpoint(t.Context(), checkpoint.SessionID, checkpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func emptyCheckpointBranch(checkpoint db.SessionCheckpoint) db.CheckpointBranch {
	return db.CheckpointBranch{
		SourceSessionID: checkpoint.SessionID, Workspace: checkpoint.Workspace,
		ExpectedSourceRevision: checkpoint.SourceRevision, CheckpointID: checkpoint.ID, CheckpointChecksum: checkpoint.Checksum,
		Child: db.SessionRecord{ID: "child", Workspace: checkpoint.Workspace, ContextJSON: []byte(`[]`)},
	}
}

func TestCheckpointRoundTripOwnershipAndRestart(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	want := bytes.Clone(checkpoint.Payload)
	got := saveCheckpoint(t, store, checkpoint)
	if got.Checksum == "" || got.CreatedAt.IsZero() || got.SourceRevision != 0 || !bytes.Equal(got.Payload, want) {
		t.Fatal("checkpoint did not retain the initial snapshot")
	}
	checkpoint.Payload[0] = 'x'
	got.Payload[0] = 'y'
	again, err := store.GetSessionCheckpoint(t.Context(), checkpoint.SessionID, checkpoint.ID)
	if err != nil || !bytes.Equal(again.Payload, want) {
		t.Fatalf("payload aliases caller memory: %v", err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), checkpoint); !errors.Is(err, db.ErrCheckpointConflict) {
		t.Fatalf("overwrite error = %v", err)
	}
	transcript, err := store.GetSessionTranscript(t.Context(), checkpoint.SessionID)
	if err != nil || transcript.Revision != 0 || len(transcript.Entries) != 0 {
		t.Fatalf("initial canonical transcript: %v", err)
	}
	if err := store.ClearSessionDraft(t.Context(), checkpoint.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(t.Context(), checkpoint.SessionID); err != nil {
		t.Fatalf("draft cleanup deleted checkpoint session: %v", err)
	}

	// Use a second connection to prove durable, independently decoded state.
	var databasePath string
	var sequence int
	var name string
	if err := store.DB().QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &databasePath); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err = reopened.GetSessionCheckpoint(t.Context(), checkpoint.SessionID, checkpoint.ID)
	if err != nil || !bytes.Equal(got.Payload, want) || got.Checksum != again.Checksum {
		t.Fatalf("restart checkpoint: %v", err)
	}
}

func TestCheckpointQuotasEvictionAndPins(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	checkpoint.Payload = bytes.Repeat([]byte("x"), db.CheckpointPayloadLimit+1)
	if err := store.SaveSessionCheckpoint(t.Context(), checkpoint); !errors.Is(err, db.ErrCheckpointQuota) {
		t.Fatalf("oversized checkpoint error = %v", err)
	}
	checkpoint.Payload = checkpoint.Payload[:db.CheckpointPayloadLimit]
	capacity := db.CheckpointSessionBudget / db.CheckpointPayloadLimit
	for i := range capacity {
		checkpoint.ID = fmt.Sprintf("checkpoint-%02d", i)
		checkpoint.Pinned = i == 0
		saveCheckpoint(t, store, checkpoint)
	}
	// Fix retention ordering without sleeps; checksum covers timestamps, so only
	// use the insertion order (timestamp, identity), which is deterministic here.
	checkpoint.ID = fmt.Sprintf("checkpoint-%02d", capacity)
	checkpoint.Pinned = false
	saveCheckpoint(t, store, checkpoint)
	if _, err := store.GetSessionCheckpoint(t.Context(), "source", "checkpoint-00"); err != nil {
		t.Fatalf("pinned checkpoint was evicted: %v", err)
	}
	if _, err := store.GetSessionCheckpoint(t.Context(), "source", "checkpoint-01"); !errors.Is(err, db.ErrCheckpointUnavailable) {
		t.Fatalf("oldest unpinned was not evicted: %v", err)
	}
	rows, err := store.ListSessionCheckpoints(t.Context(), "source")
	if err != nil || len(rows) != capacity {
		t.Fatalf("retained checkpoints = %d, %v", len(rows), err)
	}
	for _, row := range rows {
		if err := store.PinSessionCheckpoint(t.Context(), "source", row.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint.ID = "over-pinned-budget"
	if err := store.SaveSessionCheckpoint(t.Context(), checkpoint); !errors.Is(err, db.ErrCheckpointQuota) {
		t.Fatalf("pinned budget error = %v", err)
	}
	after, err := store.ListSessionCheckpoints(t.Context(), "source")
	if err != nil || len(after) != len(rows) {
		t.Fatalf("quota failure changed saved rows: %v", err)
	}
	if _, err := store.GetSessionTranscript(t.Context(), "source"); err != nil {
		t.Fatalf("eviction removed history: %v", err)
	}
}

func TestCheckpointExpiryDoesNotReviveOnActivity(t *testing.T) {
	t.Parallel()
	for _, refresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("refresh=%t", refresh), func(t *testing.T) {
			t.Parallel()
			store, checkpoint := checkpointStore(t)
			saveCheckpoint(t, store, checkpoint)
			checkpoint.ID = "pinned"
			checkpoint.Pinned = true
			saveCheckpoint(t, store, checkpoint)
			old := time.Now().Add(-db.CheckpointRetention - time.Hour)
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE sessions SET updated_at = ? WHERE id = 'source'", old.Unix()); err != nil {
				t.Fatal(err)
			}
			if refresh {
				if err := store.SaveSession(t.Context(), db.SessionRecord{ID: "source", Workspace: "/workspace"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.GetSessionCheckpoint(t.Context(), "source", "before-first"); !errors.Is(err, db.ErrCheckpointUnavailable) {
				t.Fatalf("expired checkpoint error = %v", err)
			}
			if err := store.PinSessionCheckpoint(t.Context(), "source", "before-first", true); !errors.Is(err, db.ErrCheckpointUnavailable) {
				t.Fatalf("expired pin error = %v", err)
			}
			rows, err := store.ListSessionCheckpoints(t.Context(), "source")
			if err != nil || len(rows) != 1 || rows[0].ID != "pinned" {
				t.Fatalf("expiry list = %#v, %v", rows, err)
			}
			if _, err := store.ExpireSessionCheckpoints(t.Context(), time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetSessionTranscript(t.Context(), "source"); err != nil {
				t.Fatalf("expiry removed canonical history: %v", err)
			}
			if err := store.DeleteSession(t.Context(), "source"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetSessionCheckpoint(t.Context(), "source", "pinned"); !errors.Is(err, db.ErrCheckpointUnavailable) {
				t.Fatalf("session deletion did not cascade pinned checkpoint: %v", err)
			}
		})
	}
}

func TestCheckpointCorruptionAndConflictsArePrivate(t *testing.T) {
	t.Parallel()
	const canary = "PRIVATE-checkpoint-canary"
	for _, column := range []string{"payload", "checksum", "boundary_id", "workspace", "source_session_id", "format_version", "source_revision", "created_at_ms"} {
		t.Run(column, func(t *testing.T) {
			t.Parallel()
			store, checkpoint := checkpointStore(t)
			checkpoint.Payload = []byte(canary)
			checkpoint = saveCheckpoint(t, store, checkpoint)
			var value any = canary
			if column == "payload" {
				value = []byte(canary + "changed")
			}
			if column == "format_version" || column == "source_revision" || column == "created_at_ms" {
				value = 99
			}
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_checkpoints SET "+column+" = ?", value); err != nil {
				t.Fatal(err)
			}
			_, err := store.GetSessionCheckpoint(t.Context(), "source", checkpoint.ID)
			if !errors.Is(err, db.ErrCheckpointCorrupt) || strings.Contains(err.Error(), canary) {
				t.Fatalf("corruption diagnostic = %v", err)
			}
			if err := store.CreateCheckpointBranch(t.Context(), emptyCheckpointBranch(checkpoint)); !errors.Is(err, db.ErrCheckpointCorrupt) {
				t.Fatalf("corrupt branch error = %v", err)
			}
			var count int
			if err := store.DB().QueryRowContext(t.Context(), "SELECT count(*) FROM session_checkpoints").Scan(&count); err != nil || count != 1 {
				t.Fatalf("corrupt record was rewritten/deleted: count=%d, %v", count, err)
			}
		})
	}
}

func TestCheckpointBranchPreservesSourceAndSurvivesDeletion(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	entries := []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", Revision: 1, PayloadJSON: []byte(`{"text":"historical"}`)}}
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: "source", Workspace: "/workspace", Revision: 1, Entries: entries}); err != nil {
		t.Fatal(err)
	}
	checkpoint.SourceRevision = 1
	checkpoint = saveCheckpoint(t, store, checkpoint)
	// Historical records can mutate after capture. Branch creation must use the
	// application-decoded immutable prefix, not SELECT current source entries.
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "source", Workspace: "/workspace", ExpectedRevision: 1, Revision: 2,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", Revision: 2, PayloadJSON: []byte(`{"text":"later"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveToolOutput(t.Context(), "source", db.ToolOutputRecord{ToolCallID: "retained", Output: "full output"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveToolOutput(t.Context(), "source", db.ToolOutputRecord{ToolCallID: "future", Output: "not inherited"}); err != nil {
		t.Fatal(err)
	}
	source, err := store.GetSession(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	sourceThread, err := store.GetSessionTranscript(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	branch := emptyCheckpointBranch(checkpoint)
	branch.ExpectedSourceRevision = 2
	branch.Entries = entries
	branch.ToolCallIDs = []string{"retained"}
	if err := store.CreateCheckpointBranch(t.Context(), branch); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSession(t.Context(), "source")
	if err != nil || !reflect.DeepEqual(source, after) {
		t.Fatalf("source session changed: %v", err)
	}
	afterThread, err := store.GetSessionTranscript(t.Context(), "source")
	if err != nil || !reflect.DeepEqual(sourceThread, afterThread) {
		t.Fatalf("source canonical history changed: %v", err)
	}
	if err := store.DeleteSession(t.Context(), "source"); err != nil {
		t.Fatal(err)
	}
	child, err := store.GetSessionTranscript(t.Context(), "child")
	if err != nil || child.Revision != 1 || !reflect.DeepEqual(child.Entries, entries) {
		t.Fatalf("child lost immutable prefix: %v", err)
	}
	childCheckpoint, err := store.GetSessionCheckpoint(t.Context(), "child", checkpoint.ID)
	if err != nil || !bytes.Equal(childCheckpoint.Payload, checkpoint.Payload) || childCheckpoint.SourceSessionID != "source" {
		t.Fatalf("child lost checkpoint: %v", err)
	}
	provenance, err := store.GetSessionBranch(t.Context(), "child")
	if err != nil || provenance.SourceSessionID != "source" || provenance.SourceRevision != 2 || provenance.CheckpointChecksum != checkpoint.Checksum {
		t.Fatalf("child provenance = %#v, %v", provenance, err)
	}
	if _, err := store.GetToolOutput(t.Context(), "child", "retained"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("child inherited mutable tool output: %v", err)
	}
	if _, err := store.GetToolOutput(t.Context(), "child", "future"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("future output inherited: %v", err)
	}
	active, err := store.GetSettingExact(t.Context(), "/workspace", "active_session")
	if err != nil || active != "child" {
		t.Fatalf("active child = %q, %v", active, err)
	}
}

func TestCheckpointBranchEmptyAndAtomicFailures(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"success", "stale revision", "workspace", "checksum", "existing child", "inactive source", "commit failure", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			store, checkpoint := checkpointStore(t)
			checkpoint = saveCheckpoint(t, store, checkpoint)
			branch := emptyCheckpointBranch(checkpoint)
			wantErr := db.ErrCheckpointConflict
			ctx := t.Context()
			switch scenario {
			case "success":
				wantErr = nil
			case "stale revision":
				branch.ExpectedSourceRevision++
			case "workspace":
				branch.Workspace = "/foreign"
				branch.Child.Workspace = "/foreign"
			case "checksum":
				branch.CheckpointChecksum = "stale"
			case "existing child":
				if err := store.SaveSession(t.Context(), branch.Child); err != nil {
					t.Fatal(err)
				}
			case "inactive source":
				if err := store.SetSetting(t.Context(), "/workspace", "active_session", "other"); err != nil {
					t.Fatal(err)
				}
			case "commit failure":
				// Abort the final activation statement, after all child rows exist
				// inside the transaction, to prove rollback of the entire branch.
				if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_child_activation BEFORE UPDATE ON settings WHEN NEW.value = 'child' BEGIN SELECT RAISE(ABORT, 'injected activation failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				wantErr = context.Canceled
			}
			err := store.CreateCheckpointBranch(ctx, branch)
			if scenario == "commit failure" {
				if err == nil {
					t.Fatal("injected failure succeeded")
				}
			} else if !errors.Is(err, wantErr) {
				t.Fatalf("branch error = %v, want %v", err, wantErr)
			}
			if scenario == "success" {
				if err := store.ClearSessionDraft(t.Context(), "child"); err != nil {
					t.Fatal(err)
				}
				child, err := store.GetSessionTranscript(t.Context(), "child")
				if err != nil || child.Revision != 0 || len(child.Entries) != 0 {
					t.Fatalf("empty child = %#v, %v", child, err)
				}
				return
			}
			if _, err := store.GetSessionTranscript(t.Context(), "child"); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("partial child transcript: %v", err)
			}
			if _, err := store.GetSessionBranch(t.Context(), "child"); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("partial provenance: %v", err)
			}
			if _, err := store.GetSessionCheckpoint(t.Context(), "child", checkpoint.ID); !errors.Is(err, db.ErrCheckpointUnavailable) {
				t.Fatalf("partial child checkpoint: %v", err)
			}
			if scenario != "existing child" {
				if _, err := store.GetSession(t.Context(), "child"); !errors.Is(err, db.ErrNotFound) {
					t.Fatalf("partial child session: %v", err)
				}
			}
			active, err := store.GetSettingExact(t.Context(), "/workspace", "active_session")
			wantActive := "source"
			if scenario == "inactive source" {
				wantActive = "other"
			}
			if err != nil || active != wantActive {
				t.Fatalf("active pointer changed: %q, %v", active, err)
			}
		})
	}
}
