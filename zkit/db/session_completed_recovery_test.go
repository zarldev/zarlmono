package db_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

// Completed recovery uses this same guarded commit for both transcript deltas
// and record-only saves. Transcript equality must never authorize a row rewrite.
func TestCheckpointTurnCompletedRecoveryFullRowGuard(t *testing.T) {
	for _, advance := range []bool{false, true} {
		for _, conflict := range []bool{false, true} {
			name := "record only"
			if advance {
				name = "transcript delta"
			}
			if conflict {
				name += " conflicting draft"
			}
			t.Run(name, func(t *testing.T) {
				store, checkpoint := checkpointStore(t)
				checkpoint = saveCheckpoint(t, store, checkpoint)
				before, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				version, err := store.SessionVersion(t.Context(), checkpoint.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				record := before.Session
				record.ContextJSON = []byte(`[{"role":"assistant","content":"completed context"}]`)
				record.PendingJSON = []byte(`{"version":1,"text":"current draft"}`)
				update := db.TranscriptUpdate{SessionID: record.ID, Workspace: record.Workspace,
					ExpectedRevision: before.Transcript.Revision, Revision: before.Transcript.Revision}
				if advance {
					update.Revision++
					update.Entries = []db.TranscriptEntry{{Sequence: uint64(len(before.Transcript.Entries) + 1), EntryID: "completed-recovery", Kind: "assistant_message", Revision: update.Revision, PayloadJSON: []byte(`{"text":"completed answer"}`)}}
				}
				var want error
				if conflict {
					foreign := before.Session
					foreign.PendingJSON = []byte(`{"version":1,"text":"foreign draft"}`)
					if err := store.SaveSession(t.Context(), foreign); err != nil {
						t.Fatal(err)
					}
					before, err = store.GetSessionResumeState(t.Context(), record.ID)
					if err != nil {
						t.Fatal(err)
					}
					want = db.ErrCheckpointConflict
				}
				if err := store.CommitCheckpointTurn(t.Context(), record, update, version); !errors.Is(err, want) {
					t.Fatalf("commit = %v, want %v", err, want)
				}
				after, err := store.GetSessionResumeState(t.Context(), record.ID)
				if err != nil {
					t.Fatal(err)
				}
				if conflict {
					if !reflect.DeepEqual(before, after) {
						t.Fatal("conflict partially overwrote durable state")
					}
					return
				}
				if string(after.Session.ContextJSON) != string(record.ContextJSON) || string(after.Session.PendingJSON) != string(record.PendingJSON) || after.Transcript.Revision != update.Revision {
					t.Fatal("completed recovery was not atomic")
				}
				if !advance && !reflect.DeepEqual(before.Transcript, after.Transcript) {
					t.Fatal("record-only recovery rewrote transcript")
				}
			})
		}
	}
}
