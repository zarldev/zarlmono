package main_test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/db"
)

func TestResultPersistenceFailureIsNotCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eval.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(t.Context(), `CREATE TRIGGER reject_result BEFORE INSERT ON eval_results BEGIN SELECT RAISE(FAIL, 'blocked result persistence'); END`); err != nil {
		t.Fatal(err)
	}
	tasks := filepath.Join(dir, "tasks.jsonl")
	if err := os.WriteFile(tasks, []byte(`{"instance_id":".","repo":"ignored/repo","base_commit":"ignored","language":"go"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "--tasks", tasks, "--score", "--db", path, "--run-id", "persistence-failure", "--worktree-dir", filepath.Join(dir, "work"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("CLI succeeded: %s", output)
	}
	runs, err := store.ListRecentRuns(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ScoreStatus != "failed" || runs[0].ScoreError == "" || runs[0].EndedAt == nil {
		t.Fatalf("durable lifecycle: %+v; output %s", runs, output)
	}
}
