package db_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestRecoveryBranchPreservesInactiveSourceAndChecksPreview(t *testing.T) {
	for _, scenario := range []string{"success", "no active selection", "source changed", "transcript changed", "selection changed", "selection removed", "selection created", "checkpoint changed", "missing output"} {
		t.Run(scenario, func(t *testing.T) {
			store, checkpoint := checkpointStore(t)
			checkpoint = saveCheckpoint(t, store, checkpoint)
			active := "other-session"
			if err := store.SetSetting(t.Context(), checkpoint.Workspace, "active_session", active); err != nil {
				t.Fatal(err)
			}
			if scenario == "no active selection" || scenario == "selection created" {
				active = ""
				if err := store.DeleteSetting(t.Context(), checkpoint.Workspace, "active_session"); err != nil {
					t.Fatal(err)
				}
			}
			version, err := store.SessionVersion(t.Context(), checkpoint.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			branch := emptyCheckpointBranch(checkpoint)
			want := error(nil)
			switch scenario {
			case "source changed":
				record, err := store.GetSession(t.Context(), checkpoint.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				record.PendingJSON = []byte(`"new draft"`)
				if err := store.SaveSession(t.Context(), record); err != nil {
					t.Fatal(err)
				}
				want = db.ErrCheckpointConflict
			case "transcript changed":
				if err := store.SetSetting(t.Context(), checkpoint.Workspace, "active_session", checkpoint.SessionID); err != nil {
					t.Fatal(err)
				}
				if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: checkpoint.SessionID, Workspace: checkpoint.Workspace, Revision: 1, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "new-notice", Kind: "notice", Revision: 1, PayloadJSON: []byte(`{"text":"changed"}`)}}}); err != nil {
					t.Fatal(err)
				}
				if err := store.SetSetting(t.Context(), checkpoint.Workspace, "active_session", active); err != nil {
					t.Fatal(err)
				}
				want = db.ErrCheckpointConflict
			case "selection removed":
				if err := store.DeleteSetting(t.Context(), checkpoint.Workspace, "active_session"); err != nil {
					t.Fatal(err)
				}
				want = db.ErrCheckpointConflict
			case "selection created":
				if err := store.SetSetting(t.Context(), checkpoint.Workspace, "active_session", "new-selection"); err != nil {
					t.Fatal(err)
				}
				want = db.ErrCheckpointConflict
			case "checkpoint changed":
				branch.CheckpointChecksum = "stale-preview"
				want = db.ErrCheckpointConflict
			case "selection changed":
				if err := store.SetSetting(t.Context(), checkpoint.Workspace, "active_session", "new-selection"); err != nil {
					t.Fatal(err)
				}
				want = db.ErrCheckpointConflict
			case "missing output":
				branch.ToolCallIDs = []string{"missing"}
			}
			expectedActive, activeErr := store.GetSettingExact(t.Context(), checkpoint.Workspace, "active_session")
			before, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			err = store.CreateCheckpointRecoveryBranch(t.Context(), branch, version, active)
			if !errors.Is(err, want) {
				t.Fatalf("branch=%v, want %v", err, want)
			}
			after, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("recovery changed source", err)
			}
			gotActive, err := store.GetSettingExact(t.Context(), checkpoint.Workspace, "active_session")
			if want == nil {
				if err != nil || gotActive != branch.Child.ID {
					t.Fatal("child not selected")
				}
				provenance, err := store.GetSessionBranch(t.Context(), branch.Child.ID)
				if err != nil || provenance.SourceCheckpointID != checkpoint.ID {
					t.Fatal("missing recovery provenance", err)
				}
			} else {
				if !errors.Is(err, activeErr) || gotActive != expectedActive {
					t.Fatal("rejection changed active selection")
				}
				if _, err := store.GetSession(t.Context(), branch.Child.ID); !errors.Is(err, db.ErrNotFound) {
					t.Fatal("rejected recovery left child", err)
				}
				if _, err := store.GetSessionBranch(t.Context(), branch.Child.ID); !errors.Is(err, db.ErrNotFound) {
					t.Fatal("rejected recovery left provenance", err)
				}
			}
		})
	}
}
