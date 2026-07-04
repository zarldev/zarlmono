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
	"context"
	"errors"
	"fmt"
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

	// WorktreeParent is the directory under which each task's
	// worktree is materialized (parent/<instance_id>). Cleaned up
	// after the run if KeepWorktrees is false.
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

	go func() {
		for _, s := range cfg.Specs {
			for _, d := range cfg.Drivers {
				select {
				case <-ctx.Done():
					close(work)
					return
				case work <- slot{spec: s, driver: d}:
				}
			}
		}
		close(work)
	}()

	wg.Wait()
	close(resultsCh)

	out := Results{Started: time.Now()}
	for r := range resultsCh {
		out.Records = append(out.Records, r)
	}
	out.Ended = time.Now()
	return out, nil
}

// runOne materializes one worktree and runs one driver against it.
// Pulled out so the parallel workers above stay tight.
func runOne(ctx context.Context, cfg Config, spec task.Spec, drv harness.Driver) TaskResult {
	out := TaskResult{
		InstanceID: spec.InstanceID,
		DriverName: drv.Name(),
		Language:   spec.Language,
	}

	wt, err := task.Materialize(ctx, spec, cfg.WorktreeParent, cfg.CloneCache)
	if err != nil {
		out.Result = harness.Result{Err: fmt.Errorf("materialize: %w", err)}
		return out
	}
	if !cfg.KeepWorktrees {
		// Caller decides whether to keep; default is clean up after.
		// Note: clean up happens AFTER the run records this row to the
		// results slice, but BEFORE returning to the caller. The
		// worktree path on TaskResult is informational only by then.
		defer func() { _ = removeAll(wt) }()
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
