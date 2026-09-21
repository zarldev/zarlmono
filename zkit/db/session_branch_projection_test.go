package db_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestBranchDoesNotCopyAdvancedToolProjection(t *testing.T) {
	t.Parallel()
	store, checkpoint := historyStore(t)
	ctx := t.Context()
	original := []byte(`{"tool":{"ToolCallID":"reused","Output":"A"}}`)
	if err := store.SaveToolOutput(ctx, "source", db.ToolOutputRecord{ToolCallID: "reused", Output: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{original}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(ctx, "source", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.ID, checkpoint.History = "fork", ref
	checkpoint = saveCheckpoint(t, store, checkpoint)
	if err := store.SaveToolOutput(ctx, "source", db.ToolOutputRecord{ToolCallID: "reused", Output: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{[]byte(`{"tool":{"ToolCallID":"reused","Output":"B"}}`)}); err != nil {
		t.Fatal(err)
	}
	branch := emptyCheckpointBranch(checkpoint)
	branch.ToolCallIDs = []string{"reused"}
	if err := store.CreateCheckpointBranch(ctx, branch); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetToolOutput(ctx, "child", "reused"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("branch copied mutable projection: %v", err)
	}
	if err := store.DeleteSession(ctx, "source"); err != nil {
		t.Fatal(err)
	}
	replay, err := store.ReadSessionReplay(ctx, "child")
	if err != nil || len(replay) != 1 || !bytes.Equal(replay[0], original) {
		t.Fatalf("child immutable replay = %s, %v", replay, err)
	}
}
