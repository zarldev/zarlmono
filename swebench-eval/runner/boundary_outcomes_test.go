package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/db"
	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/runner"
	"github.com/zarldev/zarlmono/swebench-eval/task"
)

func TestCancelledRunReturnsPartialResults(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results, err := runner.Run(ctx, runner.Config{
		Specs:   []task.Spec{{InstanceID: "one"}, {InstanceID: "two"}},
		Drivers: []harness.Driver{isolatedDriver{}}, WorktreeParent: t.TempDir(),
		Materialize: func(context.Context, task.Spec, string, string) (string, error) {
			return "", errors.New("local materialization error")
		},
		OnTaskComplete: func(runner.TaskResult) { cancel() },
	})
	if !errors.Is(err, context.Canceled) || len(results.Records) == 0 {
		t.Fatalf("records=%d error=%v", len(results.Records), err)
	}
}

func TestScoreVerdictKindsAndAttemptHistory(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(bin, "python")
	script := `#!/bin/sh
if [ "$1" = "-c" ]; then exit 0; fi
case "$*" in
 *--force_rebuild*) printf '{"resolved_ids":["retry"]}' > a.run-a-rebuild.json ;;
 *) printf '{"unresolved_ids":["miss"],"empty_patch_ids":["empty"],"error_ids":["retry"],"incomplete_ids":["incomplete"]}' > a.run-a.json ;;
esac
`
	if err := os.WriteFile(python, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InsertRun(t.Context(), db.RunRecord{ID: "run", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var records []runner.TaskResult
	for _, id := range []string{"miss", "empty", "retry", "incomplete", "omitted"} {
		records = append(records, runner.TaskResult{InstanceID: id, DriverName: "a"})
	}
	results := runner.Results{Records: records}
	cfg := runner.ScoreConfig{Python: python, RunID: "run", Cleanup: true, OnScoreAttempt: func(a runner.ScoreAttempt) error {
		payload, err := json.Marshal(a)
		if err != nil {
			return err
		}
		return store.AppendScoreAttempt(t.Context(), db.ScoreAttemptEvent{RunID: "run", AttemptID: a.ID, Status: a.Status.String(), RecordedAt: time.Now(), Payload: string(payload)})
	}}
	if err := runner.Score(t.Context(), &results, cfg); err == nil || results.ScoreStatus != runner.ScoreStatuses.FAILED {
		t.Fatalf("status=%v err=%v", results.ScoreStatus, err)
	}
	for _, rec := range results.Records {
		switch rec.InstanceID {
		case "miss", "empty":
			if rec.Resolved == nil || *rec.Resolved || rec.EvaluatorError != "" {
				t.Fatalf("ordinary miss: %+v", rec)
			}
		case "retry":
			if rec.Resolved == nil || !*rec.Resolved || rec.EvaluatorError != "" {
				t.Fatalf("rebuilt verdict: %+v", rec)
			}
		default:
			if rec.Resolved != nil || rec.EvaluatorError == "" {
				t.Fatalf("unknown verdict: %+v", rec)
			}
		}
	}
	events, err := store.ListScoreAttempts(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].AttemptID != events[1].AttemptID || events[2].AttemptID != events[3].AttemptID || events[0].AttemptID == events[2].AttemptID {
		t.Fatalf("attempt trail: %+v", events)
	}
	if !strings.Contains(events[1].Payload, "error_ids") || !strings.Contains(events[3].Payload, "resolved_ids") {
		t.Fatalf("original/rebuild summaries missing: %+v", events)
	}
}

func TestScoreCaptureFailureStopsBeforeNextDriver(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"python", "docker"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sentinel := errors.New("capture unavailable")
	results := runner.Results{Records: []runner.TaskResult{{InstanceID: "task", DriverName: "a"}, {InstanceID: "task", DriverName: "b"}}}
	calls := 0
	err := runner.Score(t.Context(), &results, runner.ScoreConfig{Python: filepath.Join(bin, "python"), Cleanup: true, OnScoreAttempt: func(runner.ScoreAttempt) error { calls++; return sentinel }})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("error=%v captures=%d", err, calls)
	}
}
