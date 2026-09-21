// Command eval runs the SWE-bench Multilingual comparison harness.
//
// Usage:
//
//	eval --tasks /path/to/swebench-multilingual.parquet \
//	     --drivers zarlcode \
//	     --languages go \
//	     --sample 5 \
//	     --env /home/bruno/src/monorepo/.env \
//	     --task-timeout 10m \
//	     --concurrency 2 \
//	     --score --score-python /path/to/venv/bin/python
//
// Every invocation persists to ~/.zarlcode/swebench-eval.db: one
// row in eval_runs for the invocation, one row in eval_results per
// (task, driver). The run id is printed at start (and at end) so
// consumers can query the db after.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/swebench-eval/db"
	"github.com/zarldev/zarlmono/swebench-eval/evalconfig"
	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/manifest"
	"github.com/zarldev/zarlmono/swebench-eval/report"
	"github.com/zarldev/zarlmono/swebench-eval/runner"
	"github.com/zarldev/zarlmono/swebench-eval/task"
	"github.com/zarldev/zarlmono/swebench-eval/version"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
)

func main() {
	// Sandbox shim first: when this process is the re-exec'd child of a
	// sandboxed agent shell command, ExecShim applies the kernel policy
	// and execs the real command instead of running the harness again.
	sandbox.ExecShim()
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run holds the real entrypoint so deferred cleanup (driver release, db
// close, ctx cancel) always runs: errors return up to main, which is the
// single place that exits the process.
func run() (runErr error) {
	cfg, err := evalconfig.Parse(flag.CommandLine, os.Args[1:])
	if err != nil {
		return err
	}
	if cfg.Version {
		fmt.Fprintln(os.Stdout, version.String())
		return nil
	}
	if cfg.Persistence.ExportRun != "" {
		return exportRun(cfg.Persistence)
	}

	if cfg.Input.Tasks == "" {
		fmt.Fprintln(os.Stderr, "--tasks is required")
		os.Exit(2)
	}

	specs, err := task.LoadAny(cfg.Input.Tasks)
	if err != nil {
		log.Fatalf("load tasks: %v", err)
	}
	if languages := cfg.Input.LanguageFilter(); len(languages) > 0 {
		specs = task.FilterByLanguage(specs, languages...)
	}
	if cfg.Input.Sample > 0 {
		specs = task.Sample(specs, cfg.Input.Sample)
	}
	if len(specs) == 0 {
		fmt.Fprintln(os.Stderr, "no tasks matched the given filters")
		os.Exit(2)
	}

	ablations, err := cfg.Input.Ablations()
	if err != nil {
		return fmt.Errorf("--ablations: %w", err)
	}
	drivers, err := buildDrivers(cfg, ablations)
	if err != nil {
		return fmt.Errorf("--drivers: %w", err)
	}
	if len(drivers) == 0 {
		fmt.Fprintln(os.Stderr, "no drivers configured (unknown name?)")
		os.Exit(2)
	}
	// Drivers may hold resources (the in-process driver opens zarlcode's
	// state.db once and shares the provider across tasks). Release them
	// after the run.
	defer closeDrivers(drivers)

	parent := cfg.Worktrees.Dir
	if parent == "" {
		parent = filepath.Join(os.TempDir(), fmt.Sprintf("swebench-eval-%d", time.Now().Unix()))
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("mkdir worktree parent: %w", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Persist the run + results to ~/.zarlcode/swebench-eval.db so
	// future comparisons (and the score-update step below) have a
	// stable home.
	store, err := db.Open(ctx, cfg.Persistence.DBPath)
	if err != nil {
		return fmt.Errorf("open swebench-eval db: %w", err)
	}
	defer store.Close()

	if cfg.Persistence.RunID == "" {
		cfg.Persistence.RunID = uuid.NewString()
	}
	startedAt := time.Now()
	scoreStatus := runner.ScoreStatuses.NOTREQUESTED
	if cfg.Scoring.Enabled {
		scoreStatus = runner.ScoreStatuses.PENDING
	}
	driverNames := make([]string, len(drivers))
	for i, driver := range drivers {
		driverNames[i] = driver.Name()
	}
	manifestJSON, err := manifest.Marshal(cfg, specs, driverNames)
	if err != nil {
		return err
	}
	runRec := db.RunRecord{
		ID:             cfg.Persistence.RunID,
		StartedAt:      startedAt,
		DatasetName:    cfg.Scoring.Dataset,
		LanguageFilter: cfg.Input.Languages,
		SampleSize:     len(specs),
		Drivers:        cfg.Input.Drivers,
		TaskTimeoutMs:  cfg.Execution.TaskTimeout.Milliseconds(),
		Notes:          cfg.Persistence.Notes,
		ScoreStatus:    scoreStatus.String(),
		ManifestJSON:   string(manifestJSON),
	}
	if err := store.InsertRun(ctx, runRec); err != nil {
		return fmt.Errorf("persist run: %w", err)
	}
	results := runner.Results{ScoreStatus: scoreStatus}
	var persistErr error
	// Every exit after InsertRun finalizes the durable lifecycle, including
	// pre-score failures and cancellation. The store remains open until this ends.
	defer func() {
		if runErr != nil && (results.ScoreStatus == runner.ScoreStatuses.PENDING || results.ScoreStatus == runner.ScoreStatuses.RUNNING) {
			results.ScoreStatus = runner.ScoreStatuses.FAILED
			if persistErr == nil && (errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)) {
				results.ScoreStatus = runner.ScoreStatuses.CANCELLED
			}
			results.ScoreError = runErr.Error()
		}
		persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer persistCancel()
		statusErr := store.UpdateRunScore(persistCtx, cfg.Persistence.RunID, results.ScoreStatus.String(), results.ScoreError)
		finishErr := store.FinishRun(persistCtx, cfg.Persistence.RunID, time.Now())
		runErr = errors.Join(runErr, statusErr, finishErr)
		report.Console(os.Stdout, results)
		fmt.Fprintf(os.Stdout, "\nrun_id: %s\n", cfg.Persistence.RunID)
	}()
	fmt.Fprintf(os.Stderr, "swebench-eval: run_id=%s (sample=%d drivers=%s)\n",
		cfg.Persistence.RunID, len(specs), cfg.Input.Drivers)

	// Per-task persistence: each (task, driver) result lands in
	// eval_results as it finishes, not at end-of-run. Mid-run crash
	// loses pending tasks but keeps completed ones — recoverable.
	var persistMu sync.Mutex
	runCfg := runner.Config{
		Drivers:         drivers,
		Specs:           specs,
		WorktreeParent:  parent,
		CloneCache:      cfg.Worktrees.CloneCache,
		TaskTimeout:     cfg.Execution.TaskTimeout,
		TaskConcurrency: cfg.Execution.Concurrency,
		KeepWorktrees:   cfg.Worktrees.Keep,
		OnTaskComplete: func(rec runner.TaskResult) {
			if err := persistOneResult(ctx, store, cfg.Persistence.RunID, rec); err != nil {
				persistMu.Lock()
				persistErr = errors.Join(persistErr, err)
				persistMu.Unlock()
				cancel()
			}
		},
	}

	results, err = runner.Run(ctx, runCfg)
	results.ScoreStatus = scoreStatus
	if err := errors.Join(err, persistErr, ctx.Err()); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	var scoreErr error
	if cfg.Scoring.Enabled {
		if err := store.UpdateRunScore(ctx, cfg.Persistence.RunID, runner.ScoreStatuses.RUNNING.String(), ""); err != nil {
			return err
		}
		scoreErr = runner.Score(ctx, &results, runner.ScoreConfig{
			DatasetName: cfg.Scoring.Dataset,
			RunID:       cfg.Persistence.RunID,
			MaxWorkers:  cfg.Scoring.Workers,
			WorkDir:     cfg.Scoring.WorkDir,
			Python:      cfg.Scoring.Python,
			OnScoreAttempt: func(attempt runner.ScoreAttempt) error {
				payload, err := json.Marshal(attempt)
				if err != nil {
					return fmt.Errorf("marshal scoring attempt: %w", err)
				}
				persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				return store.AppendScoreAttempt(persistCtx, db.ScoreAttemptEvent{
					RunID: cfg.Persistence.RunID, AttemptID: attempt.ID, Status: attempt.Status.String(),
					RecordedAt: time.Now(), Payload: string(payload),
				})
			},
			OnResultScored: func(rec runner.TaskResult) error {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				return store.UpdateResolved(ctx, cfg.Persistence.RunID, rec.InstanceID, rec.DriverName, rec.Resolved, rec.EvaluatorError)
			},
		})
	}
	return scoreErr
}

// buildDrivers parses the --drivers flag and instantiates the named
// adapters with shared per-driver config. Unknown names are rejected so a
// typo cannot silently change the evaluation comparison set.
func buildDrivers(cfg evalconfig.Config, ablations []harness.Ablation) ([]harness.Driver, error) {
	names := strings.Split(cfg.Input.Drivers, ",")
	out := make([]harness.Driver, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		switch name {
		case "zarlcode":
			// One driver per ablation arm (no --ablations = one baseline
			// driver). Each arm carries its own provider handle; that's
			// per-arm overhead on the shared state.db, accepted so an
			// arm's judge can't share state with another arm's loop.
			arms := ablations
			if len(arms) == 0 {
				arms = []harness.Ablation{{}}
			}
			for _, arm := range arms {
				out = append(out, &harness.ZarlcodeDriver{
					Ablation:            arm,
					EnvFile:             cfg.Zarlcode.EnvFile,
					StateDB:             cfg.Zarlcode.StateDB,
					MaxIter:             cfg.Zarlcode.MaxIter,
					ToolConcurrency:     cfg.Zarlcode.ToolConcurrency,
					ContextWindow:       cfg.Zarlcode.ContextWindow,
					StreamIdle:          cfg.Zarlcode.StreamIdle,
					IterationTimeout:    cfg.Zarlcode.IterationTimeout,
					DeadlineGrace:       cfg.Zarlcode.DeadlineGrace,
					Provider:            cfg.Zarlcode.Provider,
					Model:               cfg.Zarlcode.Model,
					CodexEffort:         cfg.Zarlcode.CodexEffort,
					LlamacppResetURL:    cfg.Zarlcode.LlamacppResetURL,
					AllowRemoteResetURL: cfg.Zarlcode.AllowRemoteResetURL,
					VerifiedAttempts:    cfg.Zarlcode.VerifiedAttempts,
					VerifyDataset:       cfg.Scoring.Dataset,
					VerifyPython:        cfg.Scoring.Python,
					VerifyWorkers:       cfg.Zarlcode.VerifyWorkers,
					VerifyWorkDir:       cfg.Zarlcode.VerifyWorkDir,
					VerifyTimeout:       cfg.Zarlcode.VerifyTimeout,
					ThreadTranscript:    cfg.Zarlcode.ThreadTranscript,
					TranscriptDir:       cfg.Zarlcode.TranscriptDir,
				})
			}
		default:
			return nil, fmt.Errorf("unknown driver %q", name)
		}
	}
	return out, nil
}

// closeDrivers releases any driver that holds resources. The in-process
// zarlcode driver opens zarlcode's state.db once; closing it releases
// the handle after the run.
func closeDrivers(drivers []harness.Driver) {
	for _, d := range drivers {
		if c, ok := d.(interface{ Close() }); ok {
			c.Close()
		}
	}
}

// persistOneResult writes a single TaskResult into eval_results.
// Called from the per-task OnTaskComplete callback so each row
// lands as soon as the harness finishes that (task, driver) pair —
// a crash halfway through the run loses pending tasks but keeps
// completed ones.
func persistOneResult(ctx context.Context, store *db.Store, runID string, rec runner.TaskResult) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	errMsg := ""
	if rec.Result.Err != nil {
		errMsg = rec.Result.Err.Error()
	}
	err := store.InsertResult(ctx, db.ResultRecord{
		RunID:               runID,
		InstanceID:          rec.InstanceID,
		DriverName:          rec.DriverName,
		Language:            rec.Language,
		WorktreePath:        rec.WorktreePath,
		Diff:                rec.Result.Diff,
		DurationMs:          rec.Result.Duration.Milliseconds(),
		Iterations:          rec.Result.Iterations,
		ToolCalls:           rec.Result.ToolCalls,
		TokensIn:            rec.Result.TokensIn,
		TokensOut:           rec.Result.TokensOut,
		TerminalReason:      rec.Result.TerminalReason,
		Error:               errMsg,
		Resolved:            rec.Resolved,
		EvaluatorError:      rec.EvaluatorError,
		Provider:            rec.Result.Provider,
		Model:               rec.Result.Model,
		GuardrailRejections: marshalRejections(rec.Result.GuardrailRejections),
		Verified:            rec.Result.Verified,
		Attempts:            rec.Result.Attempts,
		AttemptVerdicts:     marshalVerdicts(rec.Result.AttemptVerdicts),
	})
	if err != nil {
		return fmt.Errorf("persist result %s/%s: %w", rec.InstanceID, rec.DriverName, err)
	}
	return nil
}

// marshalRejections serializes the per-guardrail rejection counts for the
// guardrail_rejections column. nil stays "" (driver surfaced no transcript)
// so it is distinguishable from "scanned, zero rejections" — though the
// counter also returns nil for a clean transcript, so "" covers both today.
func marshalRejections(counts map[string]int) string {
	if counts == nil {
		return ""
	}
	data, err := json.Marshal(counts)
	if err != nil {
		return ""
	}
	return string(data)
}

// marshalVerdicts serializes the per-attempt verifier history; nil (a
// single-shot run) stays "" so unverified rows are distinguishable from a
// verified run whose goal never evaluated.
func marshalVerdicts(verdicts []harness.AttemptVerdict) string {
	if verdicts == nil {
		return ""
	}
	data, err := json.Marshal(verdicts)
	if err != nil {
		return ""
	}
	return string(data)
}
