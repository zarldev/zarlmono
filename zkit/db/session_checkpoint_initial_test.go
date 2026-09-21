package db_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestInitialSessionCheckpointAtomicPromotion(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"new", "empty draft", "insert failure", "oversized", "invalid", "foreign active", "missing active", "existing canonical", "historical context"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			store, checkpoint := checkpointStore(t)
			record, err := store.GetSession(t.Context(), checkpoint.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			var want error
			switch scenario {
			case "new":
				if err := store.DeleteSession(t.Context(), record.ID); err != nil {
					t.Fatal(err)
				}
			case "insert failure":
				if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_initial_checkpoint BEFORE INSERT ON session_checkpoints BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				checkpoint.Payload = bytes.Repeat([]byte("x"), db.CheckpointPayloadLimit+1)
				want = db.ErrCheckpointQuota
			case "invalid":
				checkpoint.BoundaryID = ""
				want = db.ErrCheckpointCorrupt
			case "foreign active":
				if err := store.SetSetting(t.Context(), record.Workspace, "active_session", "other"); err != nil {
					t.Fatal(err)
				}
			case "missing active":
				if err := store.DeleteSetting(t.Context(), record.Workspace, "active_session"); err != nil {
					t.Fatal(err)
				}
			case "existing canonical":
				saveCheckpoint(t, store, checkpoint)
				checkpoint.ID = "another"
				want = db.ErrCheckpointConflict
			case "historical context":
				record.ContextJSON = []byte(`[{"role":"user","content":"history"}]`)
				if err := store.SaveSession(t.Context(), record); err != nil {
					t.Fatal(err)
				}
				want = db.ErrCheckpointUnavailable
			}
			before, beforeErr := store.GetSessionResumeState(t.Context(), record.ID)
			activeBefore, activeErr := store.GetSettingExact(t.Context(), record.Workspace, "active_session")
			version, err := store.SessionVersion(t.Context(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			record.ContextJSON = []byte(`{"format_version":1,"revision":0,"context":[]}`)
			record.PendingJSON = []byte(`[{"text":"recovery prompt"}]`)
			err = store.SaveInitialSessionCheckpoint(t.Context(), record, checkpoint, record.ID, version)
			if scenario == "insert failure" {
				if err == nil {
					t.Fatal("injected insertion failure was ignored")
				}
			} else if !errors.Is(err, want) {
				t.Fatalf("promotion = %v, want %v", err, want)
			}
			after, afterErr := store.GetSessionResumeState(t.Context(), record.ID)
			if err != nil {
				if !errors.Is(afterErr, beforeErr) || !reflect.DeepEqual(before, after) {
					t.Fatal("rejected promotion changed source")
				}
				activeAfter, afterActiveErr := store.GetSettingExact(t.Context(), record.Workspace, "active_session")
				if activeAfter != activeBefore || !errors.Is(afterActiveErr, activeErr) {
					t.Fatal("rejected promotion changed active pointer")
				}
				if _, err := store.GetSessionCheckpoint(t.Context(), record.ID, checkpoint.ID); !errors.Is(err, db.ErrCheckpointUnavailable) {
					t.Fatalf("rejected checkpoint survived: %v", err)
				}
				return
			}
			if afterErr != nil || after.Transcript == nil || after.Transcript.Revision != 0 || len(after.Transcript.Entries) != 0 ||
				!bytes.Equal(after.Session.ContextJSON, record.ContextJSON) || !bytes.Equal(after.Session.PendingJSON, record.PendingJSON) {
				t.Fatalf("exact initial head not committed: %v", afterErr)
			}
			if _, err := store.GetSessionCheckpoint(t.Context(), record.ID, checkpoint.ID); err != nil {
				t.Fatalf("BEFORE checkpoint not committed: %v", err)
			}
		})
	}
}
