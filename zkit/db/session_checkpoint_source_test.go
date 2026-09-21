package db_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestCheckpointSourceBarrierPreservesCanonicalHistory(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"save", "stale revision", "foreign active", "global fallback"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
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
			record.ContextJSON = []byte(`[{"role":"user","content":"current head"}]`)
			record.PendingJSON = []byte(`[{"text":"source draft"}]`)
			revision := checkpoint.SourceRevision
			var want error
			switch scenario {
			case "stale revision":
				revision++
				want = db.ErrCheckpointConflict
			case "foreign active":
				if err := store.SetSetting(t.Context(), checkpoint.Workspace, "active_session", "other"); err != nil {
					t.Fatal(err)
				}
			case "global fallback":
				if err := store.DeleteSetting(t.Context(), checkpoint.Workspace, "active_session"); err != nil {
					t.Fatal(err)
				}
				if err := store.SetSetting(t.Context(), "", "active_session", checkpoint.SessionID); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SaveCheckpointSource(t.Context(), record, revision, version); !errors.Is(err, want) {
				t.Fatalf("barrier = %v, want %v", err, want)
			}
			after, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Transcript, after.Transcript) {
				t.Fatal("source canonical history changed")
			}
			if want != nil {
				if !reflect.DeepEqual(before.Session, after.Session) {
					t.Fatal("rejected barrier changed source head")
				}
			} else if string(after.Session.ContextJSON) != string(record.ContextJSON) || string(after.Session.PendingJSON) != string(record.PendingJSON) {
				t.Fatal("source head and draft were not saved together")
			}
			if _, err := store.GetSession(t.Context(), "child"); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("barrier created child: %v", err)
			}
		})
	}
}
