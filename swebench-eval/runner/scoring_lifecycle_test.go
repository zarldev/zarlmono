package runner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/runner"
)

func TestScoreUnavailableAndCancelled(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if cancelled {
			cancel()
		}
		results := runner.Results{}
		err := runner.Score(ctx, &results, runner.ScoreConfig{Python: filepath.Join(t.TempDir(), "missing"), Cleanup: true})
		cancel()
		expected := runner.ScoreStatuses.UNAVAILABLE
		if cancelled {
			expected = runner.ScoreStatuses.CANCELLED
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
		}
		if err == nil || results.ScoreStatus != expected || results.ScoreError == "" {
			t.Fatalf("status=%v err=%v", results.ScoreStatus, err)
		}
	}
}

func TestScorePersistsPartialDriversDeterministically(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(bin, "python")
	script := `#!/bin/sh
if [ "$1" = "-c" ]; then exit 0; fi
case "$*" in
 *predictions-a.json*) printf '{"resolved_ids":["task"]}' > a.run-a.json ;;
 *) exit 7 ;;
esac
`
	if err := os.WriteFile(python, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	results := runner.Results{Records: []runner.TaskResult{{InstanceID: "task", DriverName: "b"}, {InstanceID: "task", DriverName: "a"}}}
	var persisted []runner.TaskResult
	err := runner.Score(t.Context(), &results, runner.ScoreConfig{Python: python, RunID: "run", Cleanup: true, OnResultScored: func(result runner.TaskResult) error { persisted = append(persisted, result); return nil }})
	if err == nil || results.ScoreStatus != runner.ScoreStatuses.FAILED {
		t.Fatalf("score status %v err %v", results.ScoreStatus, err)
	}
	if len(persisted) != 1 || persisted[0].DriverName != "a" || persisted[0].Resolved == nil || !*persisted[0].Resolved {
		t.Fatalf("partial verdicts: %+v", persisted)
	}
	if results.Records[0].Resolved != nil {
		t.Fatal("failed evaluator invented a verdict")
	}
}
