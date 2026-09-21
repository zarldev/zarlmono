// Package runner orchestrates evaluation runs: materialize each task's
// worktree, dispatch it through every configured driver, capture
// results, and emit a report.
//
// The runner is sequential by default and parallel-capable via a
// configured concurrency cap. Parallelism is the right primitive for
// the harness comparison axis (run zarlcode + claude-code on the same
// task in parallel) AND for the task axis (run all 300 tasks across
// 8 workers). Tune via Config.
package runner

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/task"
)

// splitTestList parses the SWE-bench FAIL_TO_PASS / PASS_TO_PASS
// columns into a slice. Both loaders (JSONL, parquet) coalesce these
// into newline-joined strings — splitTestList reverses that so
// harness drivers see the structured list. Tolerant of leading
// brackets / quotes the upstream JSON sometimes carries — strips
// `[`, `]`, `"`, and stray whitespace before splitting.
func splitTestList(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.Trim(s, "[]")
	var out []string
	for raw := range strings.SplitSeq(s, "\n") {
		v := strings.TrimSpace(strings.Trim(raw, `",`))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Config controls a single evaluation run: what tasks to evaluate,
// against which drivers, with what parallelism and timeouts.
type Config struct {
	// Drivers is the set of harness adapters to evaluate. One Result
	// per (task, driver) pair lands in the output.
	Drivers []harness.Driver

	// Specs is the task definitions to evaluate. Built by task.Load
	// + optional task.FilterByLanguage / task.Sample.
	Specs []task.Spec

	// WorktreeParent holds uniquely allocated run and task/driver directories.
	// Owned directories are removed after execution unless KeepWorktrees is set.
	WorktreeParent string

	// CloneCache is an optional cache directory used by
	// task.Materialize's --reference-if-able optimization. Empty
	// disables; speeds up large-repo cloning when set.
	CloneCache string

	// TaskTimeout is the wall-clock budget per (task, driver) pair.
	// Drivers receive it on the Task struct and should respect ctx
	// cancellation. Zero disables (relies on the driver's own caps).
	TaskTimeout time.Duration

	// TaskConcurrency caps the number of (task, driver) invocations
	// in flight at once. Default 1 (sequential).
	TaskConcurrency int

	// KeepWorktrees retains the materialized worktrees after the run
	// completes. Useful for post-hoc diff inspection; consumes disk.
	KeepWorktrees bool

	// OnTaskComplete fires after each (task, driver) result lands,
	// before the next task starts. Optional. The CLI uses it to
	// persist rows incrementally so a mid-run crash doesn't lose
	// hours of harness work. The callback runs synchronously on the
	// worker that produced the result; it should be quick and
	// concurrency-safe (workers may be > 1).
	OnTaskComplete func(TaskResult)

	// Materialize optionally supplies a repository materializer. It must prepare
	// the task entirely beneath parent; nil uses task.Materialize.
	Materialize func(context.Context, task.Spec, string, string) (string, error)
}

// Run is the entry point. Materializes each spec into a worktree,
// dispatches every (task, driver) pair, collects results, returns
// a result set the report package consumes.
func Run(ctx context.Context, cfg Config) (Results, error) {
	if len(cfg.Drivers) == 0 {
		return Results{}, errors.New("no drivers configured")
	}
	if cfg.TaskConcurrency <= 0 {
		cfg.TaskConcurrency = 1
	}
	concurrency := cfg.TaskConcurrency
	started := time.Now()
	if cfg.WorktreeParent != "" {
		if err := os.MkdirAll(cfg.WorktreeParent, 0o750); err != nil {
			return Results{}, fmt.Errorf("create worktree parent: %w", err)
		}
	}
	runDir, err := os.MkdirTemp(cfg.WorktreeParent, "eval-run-")
	if err != nil {
		return Results{}, fmt.Errorf("allocate run workspace: %w", err)
	}
	cfg.WorktreeParent, err = filepath.Abs(runDir)
	if err != nil {
		_ = os.RemoveAll(runDir)
		return Results{}, fmt.Errorf("resolve run workspace: %w", err)
	}
	if !cfg.KeepWorktrees {
		defer os.RemoveAll(runDir)
	}
	if cfg.Materialize == nil {
		cfg.Materialize = task.Materialize
	}

	type slot struct {
		spec   task.Spec
		driver harness.Driver
	}
	work := make(chan slot)
	resultsCh := make(chan TaskResult, len(cfg.Specs)*len(cfg.Drivers))

	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			for s := range work {
				rec := runOne(ctx, cfg, s.spec, s.driver)
				if cfg.OnTaskComplete != nil {
					cfg.OnTaskComplete(rec)
				}
				resultsCh <- rec
			}
		})
	}

	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer close(work)
		for _, s := range cfg.Specs {
			for _, d := range cfg.Drivers {
				select {
				case <-ctx.Done():
					return
				case work <- slot{spec: s, driver: d}:
				}
			}
		}
	}()

	wg.Wait()
	<-producerDone
	close(resultsCh)

	out := Results{Started: started}
	for r := range resultsCh {
		out.Records = append(out.Records, r)
	}
	slices.SortStableFunc(out.Records, func(a, b TaskResult) int {
		if order := cmp.Compare(a.InstanceID, b.InstanceID); order != 0 {
			return order
		}
		return cmp.Compare(a.DriverName, b.DriverName)
	})
	out.Ended = time.Now()
	return out, ctx.Err()
}

// runOne materializes one worktree and runs one driver against it.
// Pulled out so the parallel workers above stay tight.
func runOne(ctx context.Context, cfg Config, spec task.Spec, drv harness.Driver) TaskResult {
	out := TaskResult{
		InstanceID: spec.InstanceID,
		DriverName: drv.Name(),
		Language:   spec.Language,
	}

	parent, err := os.MkdirTemp(cfg.WorktreeParent, "attempt-")
	if err != nil {
		out.Result = harness.Result{Err: fmt.Errorf("allocate task workspace: %w", err)}
		return out
	}
	if !cfg.KeepWorktrees {
		defer os.RemoveAll(parent)
	}
	wt, err := cfg.Materialize(ctx, spec, parent, cfg.CloneCache)
	if err != nil {
		out.Result = harness.Result{Err: fmt.Errorf("materialize: %w", err)}
		return out
	}
	out.WorktreePath = wt

	t := harness.Task{
		ID:         spec.InstanceID,
		RepoPath:   wt,
		BaseCommit: spec.BaseCommit,
		Problem:    spec.ProblemStatement,
		Hints:      spec.HintsText,
		Language:   spec.Language,
		FailToPass: splitTestList(spec.FailToPass),
		PassToPass: splitTestList(spec.PassToPass),
		Timeout:    cfg.TaskTimeout,
	}
	out.Result = drv.Run(ctx, t)
	return out
}
