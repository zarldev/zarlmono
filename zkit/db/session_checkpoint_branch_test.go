package db_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestCheckpointSelectionGuardAppliesOnlyToBranchActivation(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	checkpoint = saveCheckpoint(t, store, checkpoint)
	if err := store.DeleteSetting(t.Context(), checkpoint.Workspace, "active_session"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(t.Context(), "", "active_session", checkpoint.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCheckpointBranch(t.Context(), emptyCheckpointBranch(checkpoint)); !errors.Is(err, db.ErrCheckpointConflict) {
		t.Fatalf("branch accepted global active pointer: %v", err)
	}
	checkpoint.ID = "another"
	if err := store.SaveSessionCheckpoint(t.Context(), checkpoint); err != nil {
		t.Fatalf("capture depended on workspace selection: %v", err)
	}
	if _, err := store.GetSession(t.Context(), "child"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("global pointer failure created child: %v", err)
	}
}

func TestCheckpointDoesNotInventLegacyInitialBoundary(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	if err := store.SaveSession(t.Context(), db.SessionRecord{ID: "source", Workspace: "/workspace", ContextJSON: []byte(`[{"role":"user","content":"legacy"}]`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), checkpoint); !errors.Is(err, db.ErrCheckpointUnavailable) {
		t.Fatalf("legacy checkpoint error = %v", err)
	}
	if _, err := store.GetSessionTranscript(t.Context(), "source"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("legacy transcript was manufactured: %v", err)
	}
}

func TestCheckpointBranchDoesNotRequireMutableOutputs(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	checkpoint = saveCheckpoint(t, store, checkpoint)
	if err := store.SaveToolOutput(t.Context(), "source", db.ToolOutputRecord{ToolCallID: "present", Output: "body"}); err != nil {
		t.Fatal(err)
	}
	branch := emptyCheckpointBranch(checkpoint)
	branch.ToolCallIDs = []string{"present", "present", "absent"}
	if err := store.CreateCheckpointBranch(t.Context(), branch); err != nil {
		t.Fatal(err)
	}
	outputs, err := store.ListToolOutputsBySession(t.Context(), "child")
	if err != nil || len(outputs) != 0 {
		t.Fatalf("mutable projection copied: %v, %v", outputs, err)
	}
}

func TestCheckpointBranchProvenanceIntegrity(t *testing.T) {
	t.Parallel()
	const canary = "PRIVATE-provenance-canary"
	for _, column := range []string{"source_session_id", "source_checkpoint_id", "source_revision", "checkpoint_checksum", "checksum", "created_at_ms"} {
		t.Run(column, func(t *testing.T) {
			t.Parallel()
			store, checkpoint := checkpointStore(t)
			checkpoint = saveCheckpoint(t, store, checkpoint)
			if err := store.CreateCheckpointBranch(t.Context(), emptyCheckpointBranch(checkpoint)); err != nil {
				t.Fatal(err)
			}
			var value any = canary
			if column == "source_revision" || column == "created_at_ms" {
				value = 77
			}
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_branches SET "+column+" = ?", value); err != nil {
				t.Fatal(err)
			}
			_, err := store.GetSessionBranch(t.Context(), "child")
			if !errors.Is(err, db.ErrCheckpointCorrupt) || strings.Contains(err.Error(), canary) {
				t.Fatalf("provenance diagnostic = %v", err)
			}
			var retained any
			if err := store.DB().QueryRowContext(t.Context(), "SELECT "+column+" FROM session_branches").Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if column != "source_revision" && column != "created_at_ms" && retained != canary {
				t.Fatal("corrupt provenance was rewritten")
			}
		})
	}
}

func TestCheckpointBranchRejectsMalformedPrefix(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"wrong revision", "missing parent", "duplicate ID", "invalid JSON", "missing entry"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			store, checkpoint := checkpointStore(t)
			entries := []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Revision: 1, Kind: "user_message", PayloadJSON: []byte(`{}`)}}
			if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: "source", Workspace: "/workspace", Revision: 1, Entries: entries}); err != nil {
				t.Fatal(err)
			}
			checkpoint.SourceRevision = 1
			checkpoint = saveCheckpoint(t, store, checkpoint)
			branch := emptyCheckpointBranch(checkpoint)
			branch.Entries = entries
			switch scenario {
			case "wrong revision":
				branch.Entries[0].Revision = 2
			case "missing parent":
				branch.Entries[0].ParentID = "future"
			case "duplicate ID":
				duplicate := entries[0]
				duplicate.Sequence = 2
				branch.Entries = append(branch.Entries, duplicate)
			case "invalid JSON":
				branch.Entries[0].PayloadJSON = []byte("PRIVATE-invalid-json")
			case "missing entry":
				branch.Entries = nil
			}
			err := store.CreateCheckpointBranch(t.Context(), branch)
			if !errors.Is(err, db.ErrCheckpointCorrupt) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("invalid prefix diagnostic = %v", err)
			}
			if _, err := store.GetSession(t.Context(), "child"); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("invalid prefix created child: %v", err)
			}
		})
	}
}

func TestCheckpointConcurrentBranchesHaveOneWinner(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	checkpoint = saveCheckpoint(t, store, checkpoint)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, childID := range []string{"child-a", "child-b"} {
		wg.Go(func() {
			branch := emptyCheckpointBranch(checkpoint)
			branch.Child.ID = childID
			results <- store.CreateCheckpointBranch(t.Context(), branch)
		})
	}
	wg.Wait()
	close(results)
	var successes int
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, db.ErrCheckpointConflict) {
			t.Fatalf("losing branch error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful branches = %d, want 1", successes)
	}
	rows, err := store.ListSessions(t.Context(), "/workspace")
	if err != nil || len(rows) != 2 {
		t.Fatalf("concurrent branch session count = %d, %v", len(rows), err)
	}
}
