package db_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/db/gen"
)

func TestCheckpointSourceRejectsSameRevisionContentChange(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"draft", "context", "label", "plan", "usage", "diffs"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			store, checkpoint := checkpointStore(t)
			checkpoint = saveCheckpoint(t, store, checkpoint)
			source, err := store.GetSession(t.Context(), checkpoint.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			version, err := store.SessionVersion(t.Context(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			competing := source
			switch mutation {
			case "draft":
				competing.PendingJSON = []byte(`[{"text":"private-canary"}]`)
			case "context":
				competing.ContextJSON = []byte(`[{"role":"user","content":"private-canary"}]`)
			case "label":
				competing.Label = "private-canary"
			case "plan":
				competing.PlanJSON = []byte(`{"text":"private-canary"}`)
			case "usage":
				competing.LastUsageJSON = []byte(`{"tokens":42}`)
			case "diffs":
				competing.DiffBodiesJSON = []byte(`{"path":"private-canary"}`)
			}
			if err := store.SaveSession(t.Context(), competing); err != nil {
				t.Fatal(err)
			}
			before, err := store.GetSessionResumeState(t.Context(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveCheckpointSource(t.Context(), source, checkpoint.SourceRevision, version); !errors.Is(err, db.ErrCheckpointConflict) {
				t.Fatalf("source barrier = %v", err)
			}
			after, err := store.GetSessionResumeState(t.Context(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("competing source state overwritten")
			}
		})
	}
}

func TestInitialCheckpointRejectsCompetingDraft(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	source, err := store.GetSession(t.Context(), checkpoint.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.SessionVersion(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	competing := source
	competing.PendingJSON = []byte(`[{"text":"competing draft"}]`)
	if err := store.SaveSessionDraft(t.Context(), competing); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSessionResumeState(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveInitialSessionCheckpoint(t.Context(), source, checkpoint, source.ID, version); !errors.Is(err, db.ErrCheckpointConflict) {
		t.Fatalf("promotion = %v", err)
	}
	after, err := store.GetSessionResumeState(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("competing draft overwritten")
	}
	if _, err := store.GetSessionCheckpoint(t.Context(), source.ID, checkpoint.ID); !errors.Is(err, db.ErrCheckpointUnavailable) {
		t.Fatalf("rejected checkpoint persisted: %v", err)
	}
}

func TestSessionContentVersionDraftReceipts(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	other, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	initial, err := store.SessionVersion(t.Context(), "draft")
	if err != nil || initial != (db.SessionContentVersion{}) {
		t.Fatalf("absent version: %v", err)
	}
	record := db.SessionRecord{ID: "draft", Workspace: "/workspace", PendingJSON: []byte(`{"text":"first"}`)}
	first, err := store.SaveSessionDraftVersioned(t.Context(), record, initial)
	if err != nil || first == initial {
		t.Fatalf("create receipt: %v", err)
	}
	observed, err := other.SessionVersion(t.Context(), record.ID)
	if err != nil || observed != first {
		t.Fatalf("cross-connection observation: %v", err)
	}
	record.PendingJSON = []byte(`{"text":"second"}`)
	second, err := other.SaveSessionDraftVersioned(t.Context(), record, observed)
	if err != nil || second == first {
		t.Fatalf("update receipt: %v", err)
	}
	record.PendingJSON = []byte(`{"text":"stale"}`)
	if _, err := store.SaveSessionDraftVersioned(t.Context(), record, first); !errors.Is(err, db.ErrCheckpointConflict) {
		t.Fatalf("stale draft: %v", err)
	}
	after, err := store.SessionVersion(t.Context(), record.ID)
	if err != nil || after != second {
		t.Fatalf("stale draft changed content: %v", err)
	}
}

func TestAcquireCheckpointWriteClaimsWriter(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	other, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })

	owner, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Rollback() })
	if err := gen.New(owner).AcquireCheckpointWrite(t.Context()); err != nil {
		t.Fatalf("acquire owner: %v", err)
	}

	conn, err := other.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(t.Context(), "PRAGMA busy_timeout = 0"); err != nil {
		t.Fatal(err)
	}
	contender, err := conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = contender.Rollback() })
	if err := gen.New(contender).AcquireCheckpointWrite(t.Context()); err == nil {
		t.Fatal("second transaction acquired concurrent SQLite writer ownership")
	}
}
