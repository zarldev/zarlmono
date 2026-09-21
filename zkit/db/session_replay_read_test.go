package db_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestReadSessionReplaySharedAncestryAndOwnedOccurrences(t *testing.T) {
	t.Parallel()
	store, checkpoint := historyStore(t)
	ctx := t.Context()
	repeated := []byte(`{"output":"duplicate"}`)
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{repeated, repeated}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(ctx, "source", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.ID, checkpoint.History = "fork", ref
	checkpoint = saveCheckpoint(t, store, checkpoint)
	// The source advances before branching; only the checkpoint prefix is shared.
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{[]byte(`{"output":"source suffix"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCheckpointBranch(ctx, emptyCheckpointBranch(checkpoint)); err != nil {
		t.Fatal(err)
	}
	suffix := []byte(`{"output":"child suffix"}`)
	if err := store.AppendSessionReplay(ctx, "child", [][]byte{suffix}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(ctx, "source"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := store.ReadSessionReplay(ctx, "child")
		if err != nil || len(got) != 3 {
			t.Fatalf("read child occurrences: %d, %v", len(got), err)
		}
		if !bytes.Equal(got[0], repeated) || !bytes.Equal(got[1], repeated) || !bytes.Equal(got[2], suffix) {
			t.Fatalf("branch order or isolation: %s", got)
		}
		got[0][0] = 'x'
		if !bytes.Equal(got[1], repeated) {
			t.Fatal("duplicate occurrences alias each other")
		}
	}
	if _, err := store.ReadSessionReplay(ctx, "source"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("deleted source: %v", err)
	}
}

func TestReadSessionReplayAbsenceEmptyAndIntegrity(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	ctx := t.Context()
	if err := store.SaveSession(ctx, db.SessionRecord{ID: "legacy", Workspace: "/workspace"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"missing", "legacy"} {
		if _, err := store.ReadSessionReplay(ctx, id); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if got, err := store.ReadSessionReplay(ctx, "source"); err != nil || len(got) != 0 {
		t.Fatalf("empty canonical prefix: %s, %v", got, err)
	}
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{[]byte(`{"output":"recorded"}`)}); err != nil {
		t.Fatal(err)
	}
	// Corrupt storage through the test-only SQL escape hatch, bypassing its
	// immutable-write trigger, to exercise the public reader's integrity check.
	if _, err := store.DB().ExecContext(ctx, "DROP TRIGGER session_history_values_immutable"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, "UPDATE session_history_values SET payload = '{}' "); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadSessionReplay(ctx, "source"); !errors.Is(err, db.ErrCheckpointCorrupt) {
		t.Fatalf("corrupt occurrence: %v", err)
	}
}
