package db_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

// A subprocess uses its own SQLite connection and commits a different conversation
// while the parent retains its original source version and transcript revision.
func TestIndependentConversationWriterProcess(t *testing.T) {
	path := os.Getenv("ZARLCODE_TEST_INDEPENDENT_DB")
	if path == "" {
		t.Skip("subprocess helper")
	}
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	saveIndependentConversation(t, store)
}

func saveIndependentConversation(t *testing.T, store *db.Store) {
	t.Helper()
	record := db.SessionRecord{ID: "other", Workspace: "/workspace", ContextJSON: []byte(`[]`)}
	update := db.TranscriptUpdate{SessionID: record.ID, Workspace: record.Workspace, Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "other-message", Kind: "user_message", Revision: 1, PayloadJSON: []byte(`{"text":"other conversation"}`)}}}
	if err := store.CommitCompletedTurn(t.Context(), record, update); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentConversationCheckpointWrites(t *testing.T) {
	t.Parallel()
	for _, writer := range []string{"connection", "process"} {
		for _, operation := range []string{"initial", "before", "settlement", "settlement without delta"} {
			t.Run(writer+"/"+operation, func(t *testing.T) {
				t.Parallel()
				path := filepath.Join(t.TempDir(), "state.db")
				store, err := db.Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				record := db.SessionRecord{ID: "source", Workspace: "/workspace", ContextJSON: []byte(`[]`)}
				update := db.TranscriptUpdate{SessionID: record.ID, Workspace: record.Workspace}
				checkpoint := db.SessionCheckpoint{SessionID: record.ID, SourceSessionID: record.ID,
					Workspace: record.Workspace, ID: "before", BoundaryID: "prompt", FormatVersion: 2}
				version := db.SessionContentVersion{}
				if operation != "initial" {
					version, err = store.CommitBeforeTurnVersioned(t.Context(), record, update,
						preparedHistoryBoundary(checkpoint, nil), "", version)
					if err != nil {
						t.Fatal(err)
					}
					checkpoint.ID = "next-before"
				}
				if writer == "connection" {
					other, err := db.Open(t.Context(), path)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = other.Close() })
					saveIndependentConversation(t, other)
				} else {
					executable, err := os.Executable()
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, executable, "-test.run=^TestIndependentConversationWriterProcess$")
					cmd.Env = append(os.Environ(), "ZARLCODE_TEST_INDEPENDENT_DB="+path)
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("other process: %v\n%s", err, output)
					}
				}
				otherBefore, err := store.GetSessionResumeState(t.Context(), "other")
				if err != nil {
					t.Fatal(err)
				}
				record.PendingJSON = []byte(`{"text":"preserved input"}`)
				switch operation {
				case "initial", "before":
					_, err = store.CommitBeforeTurnVersioned(t.Context(), record, update,
						preparedHistoryBoundary(checkpoint, nil), "", version)
				default:
					if operation == "settlement" {
						update.Revision = 1
						update.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "answer", Kind: "assistant_message", Revision: 1, PayloadJSON: []byte(`{"text":"completed work"}`)}}
					}
					_, err = store.CommitCheckpointTurnVersioned(t.Context(), record, update, version)
				}
				if err != nil {
					t.Fatalf("unrelated conversation blocked %s: %v", operation, err)
				}
				saved, err := store.GetSessionResumeState(t.Context(), record.ID)
				if err != nil || string(saved.Session.PendingJSON) != string(record.PendingJSON) || saved.Transcript.Revision != update.Revision {
					t.Fatalf("source work not saved: %v", err)
				}
				otherAfter, err := store.GetSessionResumeState(t.Context(), "other")
				if err != nil || !reflect.DeepEqual(otherBefore, otherAfter) {
					t.Fatalf("unrelated conversation changed: %v", err)
				}
			})
		}
	}
}
