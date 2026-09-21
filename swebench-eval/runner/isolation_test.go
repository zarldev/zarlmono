package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/runner"
	"github.com/zarldev/zarlmono/swebench-eval/task"
)

type isolatedDriver struct {
	name    string
	entered chan<- string
	release <-chan struct{}
}

func (d isolatedDriver) Name() string { return d.name }
func (d isolatedDriver) Run(ctx context.Context, task harness.Task) harness.Result {
	path := filepath.Join(task.RepoPath, "sentinel")
	if err := os.WriteFile(path, []byte(d.name), 0600); err != nil {
		return harness.Result{Err: err}
	}
	select {
	case d.entered <- task.RepoPath:
	case <-ctx.Done():
		return harness.Result{Err: ctx.Err()}
	}
	select {
	case <-d.release:
	case <-ctx.Done():
		return harness.Result{Err: ctx.Err()}
	}
	value, err := os.ReadFile(path)
	return harness.Result{Diff: string(value), Err: err}
}

func TestConcurrentRunsIsolateTaskDriverWorkspaces(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup", true: "retained"}[keep], func(t *testing.T) {
			entered := make(chan string, 4)
			release := make(chan struct{})
			cfg := runner.Config{WorktreeParent: t.TempDir(), KeepWorktrees: keep, TaskConcurrency: 2,
				Specs:   []task.Spec{{InstanceID: "same-task"}},
				Drivers: []harness.Driver{isolatedDriver{"z", entered, release}, isolatedDriver{"a", entered, release}},
				Materialize: func(_ context.Context, _ task.Spec, parent, _ string) (string, error) {
					path := filepath.Join(parent, "repo")
					return path, os.Mkdir(path, 0700)
				},
			}
			var wg sync.WaitGroup
			results := make([]runner.Results, 2)
			errs := make([]error, 2)
			for i := range 2 {
				wg.Go(func() { results[i], errs[i] = runner.Run(t.Context(), cfg) })
			}
			paths := make(map[string]bool)
			for range 4 {
				path := <-entered
				if paths[path] {
					t.Error("workspace shared by multiple executions")
				}
				paths[path] = true
			}
			close(release)
			wg.Wait()
			for i, result := range results {
				if errs[i] != nil {
					t.Fatal(errs[i])
				}
				if len(result.Records) != 2 {
					t.Fatalf("records=%d", len(result.Records))
				}
				if result.Records[0].DriverName != "a" || result.Records[1].DriverName != "z" {
					t.Fatal("unstable driver order")
				}
				for _, rec := range result.Records {
					if rec.Result.Err != nil || rec.Result.Diff != rec.DriverName {
						t.Fatalf("worktree interference: %+v", rec)
					}
				}
			}
			for path := range paths {
				_, err := os.Stat(path)
				if keep && err != nil {
					t.Fatal(err)
				}
				if !keep && !os.IsNotExist(err) {
					t.Fatalf("owned workspace not removed: %s", path)
				}
			}
		})
	}
}
