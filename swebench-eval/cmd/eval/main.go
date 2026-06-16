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
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/swebench-eval/db"
	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/report"
	"github.com/zarldev/zarlmono/swebench-eval/runner"
	"github.com/zarldev/zarlmono/swebench-eval/task"
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
func run() error {
	tasksPath := flag.String("tasks", "", "path to SWE-bench JSONL or Parquet task file (required)")
	driversFlag := flag.String("drivers", "zarlcode", "comma-separated list of drivers to run (zarlcode)")
	ablationsFlag := flag.String("ablations", "", "comma-separated zarlcode guardrail-ablation arms, or \"all\" (baseline, no-shell, no-skill-hint, no-decompose, no-fanout, no-test-edit, no-improvement, judge); each arm runs as its own driver")
	langFlag := flag.String("languages", "", "comma-separated language filter (empty = all)")
	sampleN := flag.Int("sample", 0, "stratified sample size (0 = run all matching specs)")
	envFile := flag.String("env", "", "path to .env loaded before provider construction (carries per-backend URL/key knobs)")
	zarlcodeProvider := flag.String("zarlcode-provider", "", "pin zarlcode's backend (registry name: llamacpp, openai-codex, gemini, claude-code, …); empty = registry default (llamacpp)")
	zarlcodeModel := flag.String("zarlcode-model", "", "pin zarlcode's model (e.g. gpt-5.5, qwen3.6-35b-a3b-mtp); empty = provider's default model")
	zarlcodeCodexEffort := flag.String("zarlcode-codex-effort", "", "codex_reasoning_effort when provider is openai-codex (low/medium/high/xhigh)")
	llamacppResetURL := flag.String("llamacpp-reset-url", "", "POSTed before each task to flush local llama-server's KV cache slot; e.g. http://localhost:8081/slots/0?action=erase (requires --slot-save-path on the server)")
	allowRemoteResetURL := flag.Bool("allow-remote-reset-url", false, "permit a non-loopback --llamacpp-reset-url (default: loopback only, to avoid SSRF via a misconfigured URL)")
	stateDB := flag.String("state-db", "", "path to zarlcode state.db — vault + custom-provider rows (empty = $HOME/.zarlcode/state.db)")
	taskTimeout := flag.Duration("task-timeout", 5*time.Minute, "wall-clock budget per (task, driver)")
	maxIter := flag.Int("max-iter", 0, "cap the agent loop's iterations (0 = loop default)")
	toolConcurrency := flag.Int("tool-concurrency", 0, "cap concurrent tool dispatch per iteration (0 = sequential)")
	contextWindow := flag.Int("context-window", 0, "compactor context-window size in tokens (0 = 32768)")
	zarlcodeVerifiedAttempts := flag.Int("zarlcode-verified-attempts", 0, "enable zarlcode harness re-drive with SWE-bench verifier; values >1 cap attempts, 0/1 = trust terminal reason")
	zarlcodeVerifyWorkers := flag.Int("zarlcode-verify-workers", 1, "SWE-bench evaluator workers for per-attempt zarlcode verification")
	zarlcodeVerifyWorkDir := flag.String("zarlcode-verify-workdir", "", "directory for per-attempt zarlcode verification logs (empty = tempdir per attempt)")
	zarlcodeVerifyTimeout := flag.Duration("zarlcode-verify-timeout", 0, "per-attempt SWE-bench verifier timeout, independent of the agent task timeout (0 = 30m)")
	zarlcodeThreadTranscript := flag.Bool("zarlcode-thread-transcript", false, "verified re-drives carry the full prior transcript (needs a large --context-window); default re-drives with verifier feedback only")
	zarlcodeTranscriptDir := flag.String("zarlcode-transcript-dir", "", "persist each task's full agent transcript to <dir>/<instance_id>.json for post-hoc debugging (empty = disabled)")
	concurrency := flag.Int("concurrency", 1, "parallel (task, driver) invocations")
	worktreeDir := flag.String("worktree-dir", "", "where to materialize worktrees (empty = a fresh tempdir)")
	cloneCache := flag.String("clone-cache", "", "optional --reference clone cache directory")
	keepWorktrees := flag.Bool("keep-worktrees", false, "leave worktrees on disk after the run for post-hoc inspection")
	score := flag.Bool("score", false, "after the harness loop, invoke SWE-bench's evaluator on each driver's diffs and report resolved/unresolved")
	scoreDataset := flag.String("score-dataset", "SWE-bench/SWE-bench_Multilingual", "dataset name passed to the SWE-bench evaluator")
	scoreWorkers := flag.Int("score-workers", 4, "SWE-bench evaluator --max_workers")
	scoreWorkDir := flag.String("score-workdir", "", "directory for the evaluator's predictions + logs (empty = a fresh tempdir)")
	scorePython := flag.String("score-python", "", "python interpreter that has the swebench package importable (empty = python3 on PATH; typical: a venv's bin/python)")
	dbPath := flag.String("db", "", "path to swebench-eval sqlite (empty = $HOME/.zarlcode/swebench-eval.db)")
	runID := flag.String("run-id", "", "explicit run id (empty = a generated uuid)")
	runNotes := flag.String("run-notes", "", "free-form notes to attach to the run row — eg. 'after decompose advisory refactor'")
	flag.Parse()

	if *tasksPath == "" {
		fmt.Fprintln(os.Stderr, "--tasks is required")
		os.Exit(2)
	}

	specs, err := task.LoadAny(*tasksPath)
	if err != nil {
		log.Fatalf("load tasks: %v", err)
	}
	if *langFlag != "" {
		langs := strings.Split(*langFlag, ",")
		specs = task.FilterByLanguage(specs, langs...)
	}
	if *sampleN > 0 {
		specs = task.Sample(specs, *sampleN)
	}
	if len(specs) == 0 {
		fmt.Fprintln(os.Stderr, "no tasks matched the given filters")
		os.Exit(2)
	}

	ablations, err := harness.AblationArms(*ablationsFlag)
	if err != nil {
		return fmt.Errorf("--ablations: %w", err)
	}
	drivers := buildDrivers(driverBuildOpts{
		spec:                *driversFlag,
		ablations:           ablations,
		envFile:             *envFile,
		stateDB:             *stateDB,
		maxIter:             *maxIter,
		toolConcurrency:     *toolConcurrency,
		contextWindow:       *contextWindow,
		provider:            *zarlcodeProvider,
		model:               *zarlcodeModel,
		codexEffort:         *zarlcodeCodexEffort,
		llamacppResetURL:    *llamacppResetURL,
		allowRemoteResetURL: *allowRemoteResetURL,
		verifiedAttempts:    *zarlcodeVerifiedAttempts,
		verifyDataset:       *scoreDataset,
		verifyPython:        *scorePython,
		verifyWorkers:       *zarlcodeVerifyWorkers,
		verifyWorkDir:       *zarlcodeVerifyWorkDir,
		verifyTimeout:       *zarlcodeVerifyTimeout,
		threadTranscript:    *zarlcodeThreadTranscript,
		transcriptDir:       *zarlcodeTranscriptDir,
	})
	if len(drivers) == 0 {
		fmt.Fprintln(os.Stderr, "no drivers configured (unknown name?)")
		os.Exit(2)
	}
	// Drivers may hold resources (the in-process driver opens zarlcode's
	// state.db once and shares the provider across tasks). Release them
	// after the run.
	defer closeDrivers(drivers)

	parent := *worktreeDir
	if parent == "" {
		parent = filepath.Join(os.TempDir(), fmt.Sprintf("swebench-eval-%d", time.Now().Unix()))
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("mkdir worktree parent: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Persist the run + results to ~/.zarlcode/swebench-eval.db so
	// future comparisons (and the score-update step below) have a
	// stable home.
	store, err := db.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open swebench-eval db: %w", err)
	}
	defer store.Close()

	if *runID == "" {
		*runID = uuid.NewString()
	}
	startedAt := time.Now()
	runRec := db.RunRecord{
		ID:             *runID,
		StartedAt:      startedAt,
		DatasetName:    *scoreDataset,
		LanguageFilter: *langFlag,
		SampleSize:     len(specs),
		Drivers:        *driversFlag,
		TaskTimeoutMs:  taskTimeout.Milliseconds(),
		Notes:          *runNotes,
	}
	if err := store.InsertRun(ctx, runRec); err != nil {
		return fmt.Errorf("persist run: %w", err)
	}
	fmt.Fprintf(os.Stderr, "swebench-eval: run_id=%s (sample=%d drivers=%s)\n",
		*runID, len(specs), *driversFlag)

	// Per-task persistence: each (task, driver) result lands in
	// eval_results as it finishes, not at end-of-run. Mid-run crash
	// loses pending tasks but keeps completed ones — recoverable.
	cfg := runner.Config{
		Drivers:         drivers,
		Specs:           specs,
		WorktreeParent:  parent,
		CloneCache:      *cloneCache,
		TaskTimeout:     *taskTimeout,
		TaskConcurrency: *concurrency,
		KeepWorktrees:   *keepWorktrees,
		OnTaskComplete: func(rec runner.TaskResult) {
			persistOneResult(ctx, store, *runID, rec)
		},
	}

	results, err := runner.Run(ctx, cfg)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	// Per-task persistence already landed each row via OnTaskComplete;
	// the post-loop persistResults is now belt-and-suspenders for any
	// missed callbacks. INSERT OR IGNORE on the PK would be cleaner,
	// but the row is idempotent enough — re-inserting the same key
	// errors out and we log+continue.

	if *score {
		if scoreErr := runner.Score(ctx, &results, runner.ScoreConfig{
			DatasetName: *scoreDataset,
			MaxWorkers:  *scoreWorkers,
			WorkDir:     *scoreWorkDir,
			Python:      *scorePython,
		}); scoreErr != nil {
			fmt.Fprintln(os.Stderr, "score:", scoreErr)
		} else {
			persistResolved(ctx, store, *runID, results)
		}
	}

	if err := store.FinishRun(ctx, *runID, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "finish run:", err)
	}

	report.Console(os.Stdout, results)
	fmt.Fprintf(os.Stdout, "\nrun_id: %s\n", *runID)
	return nil
}

// driverBuildOpts groups the (already-7-field, growing) parameters
// the driver factories need. Adding a new flag → new field here,
// flow through buildDrivers, no per-driver constructor surgery.
type driverBuildOpts struct {
	spec                string
	ablations           []harness.Ablation
	envFile             string
	stateDB             string
	maxIter             int
	toolConcurrency     int
	contextWindow       int
	provider            string
	model               string
	codexEffort         string
	llamacppResetURL    string
	allowRemoteResetURL bool
	verifiedAttempts    int
	verifyDataset       string
	verifyPython        string
	verifyWorkers       int
	verifyWorkDir       string
	verifyTimeout       time.Duration
	threadTranscript    bool
	transcriptDir       string
}

// buildDrivers parses the --drivers flag and instantiates the named
// adapters with shared per-driver config. Unknown names are silently
// dropped so a typo doesn't bring down the whole run — instead the
// "no drivers configured" check upstream catches the empty result.
func buildDrivers(o driverBuildOpts) []harness.Driver {
	names := strings.Split(o.spec, ",")
	out := make([]harness.Driver, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		switch name {
		case "zarlcode":
			// One driver per ablation arm (no --ablations = one baseline
			// driver). Each arm carries its own provider handle; that's
			// per-arm overhead on the shared state.db, accepted so an
			// arm's judge can't share state with another arm's loop.
			arms := o.ablations
			if len(arms) == 0 {
				arms = []harness.Ablation{{}}
			}
			for _, arm := range arms {
				out = append(out, &harness.ZarlcodeDriver{
					Ablation:            arm,
					EnvFile:             o.envFile,
					StateDB:             o.stateDB,
					MaxIter:             o.maxIter,
					ToolConcurrency:     o.toolConcurrency,
					ContextWindow:       o.contextWindow,
					Provider:            o.provider,
					Model:               o.model,
					CodexEffort:         o.codexEffort,
					LlamacppResetURL:    o.llamacppResetURL,
					AllowRemoteResetURL: o.allowRemoteResetURL,
					VerifiedAttempts:    o.verifiedAttempts,
					VerifyDataset:       o.verifyDataset,
					VerifyPython:        o.verifyPython,
					VerifyWorkers:       o.verifyWorkers,
					VerifyWorkDir:       o.verifyWorkDir,
					VerifyTimeout:       o.verifyTimeout,
					ThreadTranscript:    o.threadTranscript,
					TranscriptDir:       o.transcriptDir,
				})
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown driver %q (skipping)\n", name)
		}
	}
	return out
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
func persistOneResult(ctx context.Context, store *db.Store, runID string, rec runner.TaskResult) {
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
		fmt.Fprintf(os.Stderr, "persist result %s/%s: %v\n", rec.InstanceID, rec.DriverName, err)
	}
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

// persistResolved patches the eval_results rows with the scorer's
// verdict after Score returns. Separate from persistResults so the
// initial row exists even if scoring blows up — easier to retry
// scoring later against a complete result set.
func persistResolved(ctx context.Context, store *db.Store, runID string, r runner.Results) {
	for _, rec := range r.Records {
		if rec.Resolved == nil && rec.EvaluatorError == "" {
			continue
		}
		err := store.UpdateResolved(ctx, runID, rec.InstanceID, rec.DriverName, rec.Resolved, rec.EvaluatorError)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update resolved %s/%s: %v\n", rec.InstanceID, rec.DriverName, err)
		}
	}
}
