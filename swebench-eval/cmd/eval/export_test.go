package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/db"
	"github.com/zarldev/zarlmono/swebench-eval/task"
)

func TestCLIRecordsSelectedInputsAndExportsWithoutExecution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eval.db")
	tasksPath := filepath.Join(dir, "tasks.jsonl")
	// Both selected identifiers fail local workspace validation, before any clone
	// or provider call. The Rust task must not appear in the recorded selection.
	data := `{"instance_id":".","repo":"owner/repo","base_commit":"first","language":"go"}
{"instance_id":"ignored","repo":"owner/other","base_commit":"other","language":"rust"}
{"instance_id":"..","repo":"owner/repo","base_commit":"second","language":"go"}
`
	if err := os.WriteFile(tasksPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	run := exec.CommandContext(t.Context(), "go", "run", ".", "--tasks", tasksPath,
		"--languages", "go", "--ablations", "judge,baseline", "--max-iter", "17",
		"--db", path, "--run-id", "captured", "--worktree-dir", filepath.Join(dir, "work"))
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("fixture run: %v\n%s", err, out)
	}
	// Export is independent of the original task file and all execution settings.
	if err := os.Remove(tasksPath); err != nil {
		t.Fatal(err)
	}
	export := exec.CommandContext(t.Context(), "go", "run", ".", "--export-run", "captured",
		"--db", path, "--tasks", tasksPath, "--env", filepath.Join(dir, "missing-env"), "--drivers", "invalid")
	out, err := export.Output()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var got struct {
		FormatVersion int `json:"format_version"`
		Manifest      struct {
			FormatVersion int         `json:"format_version"`
			Tasks         []task.Spec `json:"tasks"`
			Drivers       []string    `json:"drivers"`
			Requested     struct {
				MaxIterations int `json:"max_iterations"`
			} `json:"requested"`
		} `json:"manifest"`
		Results []struct {
			InstanceID string `json:"instance_id"`
			DriverName string `json:"driver_name"`
			Error      string `json:"error"`
			Resolved   *bool  `json:"resolved"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if got.FormatVersion != 1 || got.Manifest.FormatVersion != 1 || len(got.Manifest.Tasks) != 2 || got.Manifest.Requested.MaxIterations != 17 {
		t.Fatalf("captured manifest: %+v", got.Manifest)
	}
	if got.Manifest.Tasks[0].BaseCommit != "first" || got.Manifest.Tasks[1].BaseCommit != "second" || strings.Join(got.Manifest.Drivers, ",") != "zarlcode-judge,zarlcode" {
		t.Fatalf("selection/arms changed: %+v", got.Manifest)
	}
	if len(got.Results) != 4 || got.Results[0].InstanceID != "." || got.Results[0].DriverName != "zarlcode" {
		t.Fatalf("result order/count: %+v", got.Results)
	}
	for _, result := range got.Results {
		if result.Error == "" || result.Resolved != nil {
			t.Fatalf("failed task was lost or scored: %+v", result)
		}
	}
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runs, err := store.ListRecentRuns(t.Context(), 10)
	if err != nil || len(runs) != 1 || runs[0].EndedAt == nil {
		t.Fatalf("export started another run or changed lifecycle: %+v %v", runs, err)
	}
}

func TestCLIExportMissingDatabaseDoesNotCreateIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "--export-run", "missing", "--db", path)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil || out.Len() != 0 {
		t.Fatalf("missing export succeeded or wrote stdout: %v %q", err, out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("export created a database: %v", err)
	}
}
