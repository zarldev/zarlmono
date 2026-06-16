package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointsRestoreModifiedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCheckpoints(dir)
	c.StartTurn("turn-1", 1, time.Now())
	c.RecordMutation(WorkingSetMutation{Path: "file.txt", TurnID: "turn-1", Additions: 1, Deletions: 1}, []byte("before\n"), false, []byte("after\n"), false)
	c.CompleteTurn("turn-1", time.Now())

	if err := c.RestoreTurn("turn-1"); err != nil {
		t.Fatalf("restore turn: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before\n" {
		t.Fatalf("restored content = %q, want before", got)
	}
}

func TestCheckpointsRestoreNewFileByDeletingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.txt")
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCheckpoints(dir)
	c.StartTurn("turn-1", 1, time.Now())
	c.RecordMutation(WorkingSetMutation{Path: "fresh.txt", TurnID: "turn-1", Additions: 1}, nil, true, []byte("new\n"), false)
	c.CompleteTurn("turn-1", time.Now())

	if err := c.RestoreTurn("turn-1"); err != nil {
		t.Fatalf("restore turn: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restored new file stat err = %v, want not exist", err)
	}
}

func TestCheckpointsRestoreDeletedFileByRecreatingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	c := NewCheckpoints(dir)
	c.StartTurn("turn-1", 1, time.Now())
	c.RecordMutation(WorkingSetMutation{Path: "gone.txt", TurnID: "turn-1", Deletions: 1}, []byte("old\n"), false, nil, true)
	c.CompleteTurn("turn-1", time.Now())

	if err := c.RestoreTurn("turn-1"); err != nil {
		t.Fatalf("restore turn: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\n" {
		t.Fatalf("restored content = %q, want old", got)
	}
}

func TestCheckpointsRefuseRollbackWhenCurrentContentChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCheckpoints(dir)
	c.StartTurn("turn-1", 1, time.Now())
	c.RecordMutation(WorkingSetMutation{Path: "file.txt", TurnID: "turn-1"}, []byte("before\n"), false, []byte("after\n"), false)
	c.CompleteTurn("turn-1", time.Now())

	plan, err := c.PlanRestoreTurn("turn-1")
	if err != nil {
		t.Fatalf("plan restore: %v", err)
	}
	if !plan.Conflict || len(plan.Files) != 1 || !plan.Files[0].Conflict {
		t.Fatalf("plan conflict = %+v, want one conflicted file", plan)
	}
	if err := c.RestoreTurn("turn-1"); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("restore error = %v, want ErrCheckpointConflict", err)
	}
}

func TestSessionCheckpointRecordsRichDiffMessages(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("~/repo", dir, "main")
	ordinal := s.workingSet().StartTurn("turn-1")
	s.checkpoints().StartTurn("turn-1", ordinal, time.Now())
	mutation := s.workingSet().RecordDiff("file.txt", "@@\n-old\n+new")
	s.checkpoints().RecordMutation(mutation, []byte("old\n"), false, []byte("new\n"), false)
	s.workingSet().CompleteTurn("turn-1")
	s.checkpoints().CompleteTurn("turn-1", time.Now())

	cp, ok := s.Checkpoints.CheckpointForTurn("turn-1")
	if !ok {
		t.Fatal("checkpoint missing for turn-1")
	}
	fc := cp.Files["file.txt"]
	if string(fc.Before.Content) != "old\n" || string(fc.After.Content) != "new\n" {
		t.Fatalf("checkpoint images = before %q after %q", fc.Before.Content, fc.After.Content)
	}
}
