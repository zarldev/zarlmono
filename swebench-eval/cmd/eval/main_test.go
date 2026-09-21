package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/db"
)

func TestUnavailableScoringFinalizesRunAndExitsNonzero(t *testing.T) {
	dir := t.TempDir()
	tasks := filepath.Join(dir, "tasks.jsonl")
	// Rejected locally before clone/provider setup; the task failure is still durable.
	if err := os.WriteFile(tasks, []byte(`{"instance_id":".","repo":"ignored/repo","base_commit":"ignored","language":"go"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(dir, "python")
	if err := os.WriteFile(python, []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "eval.db")
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "--tasks", tasks, "--score", "--score-python", python, "--db", path, "--run-id", "unavailable-cli", "--worktree-dir", filepath.Join(dir, "work"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("CLI succeeded: %s", output)
	}
	if !strings.Contains(string(output), "unavailable") {
		t.Fatalf("missing unavailable report: %s", output)
	}
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runs, err := store.ListRecentRuns(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != "unavailable-cli" || runs[0].ScoreStatus != "unavailable" || runs[0].ScoreError == "" || runs[0].EndedAt == nil {
		t.Fatalf("durable lifecycle: %+v; output %s", runs, output)
	}
}
