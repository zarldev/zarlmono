package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/zarldev/zarlmono/swebench-eval/internal/evaluator"
)

// ScoreConfig controls how the SWE-bench official evaluator is
// invoked to grade a Results set. The evaluator is a Python tool +
// Docker; this wrapper translates Results into the evaluator's
// expected shapes and parses its output back into per-record fields.
type ScoreConfig struct {
	// DatasetName is what the swebench evaluator's --dataset_name
	// receives. Defaults to "SWE-bench/SWE-bench_Multilingual".
	DatasetName string

	// RunID is a short identifier the evaluator uses for its output
	// directory layout. Default is "eval-<unix-timestamp>". Stable
	// per run so re-runs against the same input land in a separate
	// dir.
	RunID string

	// Python is the python interpreter to invoke. Empty -> "python3".
	Python string

	// MaxWorkers caps the evaluator's per-task parallelism. 0 -> 4.
	MaxWorkers int

	// WorkDir is where temporary predictions files + evaluator logs
	// land. Empty -> a fresh tempdir per Score call.
	WorkDir string

	// Cleanup, when true, removes WorkDir after the score completes.
	// Default false (keep for debugging).
	Cleanup bool
}

// Score takes a Results set and runs the SWE-bench evaluator against
// the diffs each driver produced. Updates each TaskResult in place
// with the evaluator's "resolved" verdict. Returns nil only when the
// evaluator ran cleanly — non-nil err means we never got verdicts
// and the resolved fields should be treated as unknown.
//
// Behavior contract:
//   - One predictions.json is generated per driver name. The evaluator
//     can't compare drivers in one run; it scores each driver's
//     submission against the same task set.
//   - Records with empty Diff are still submitted (model_patch="") so
//     the evaluator records them as not-resolved rather than
//     silently dropping. Errored records (Err != nil) are skipped —
//     they represent driver-level failures, not unsuccessful agent
//     outcomes.
//   - The evaluator's per-task output (run_id/<instance_id>/) is
//     left on disk in cfg.WorkDir if Cleanup is false, so individual
//     failures can be diffed against the gold patch by hand.
func Score(ctx context.Context, r *Results, cfg ScoreConfig) error {
	if r == nil {
		return errors.New("nil results")
	}
	if cfg.DatasetName == "" {
		cfg.DatasetName = "SWE-bench/SWE-bench_Multilingual"
	}
	if cfg.RunID == "" {
		cfg.RunID = fmt.Sprintf("eval-%d", r.Started.Unix())
	}
	if cfg.Python == "" {
		cfg.Python = "python3"
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 4
	}
	workDir := cfg.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.MkdirTemp("", "swebench-score-*")
		if err != nil {
			return fmt.Errorf("mkdir tempdir: %w", err)
		}
		if cfg.Cleanup {
			defer os.RemoveAll(workDir)
		}
	} else if err := os.MkdirAll(workDir, 0o750); err != nil {
		return fmt.Errorf("mkdir workdir: %w", err)
	}

	if err := evaluator.EnsureAvailable(ctx, cfg.Python); err != nil {
		return err
	}

	// Group records by driver — one predictions file per harness.
	byDriver := map[string][]*TaskResult{}
	for i := range r.Records {
		rec := &r.Records[i]
		if rec.Result.Err != nil {
			continue // driver-level failures aren't predictions
		}
		byDriver[rec.DriverName] = append(byDriver[rec.DriverName], rec)
	}
	for driver, recs := range byDriver {
		predsPath := filepath.Join(workDir, fmt.Sprintf("predictions-%s.json", driver))
		if err := writePredictions(predsPath, driver, recs); err != nil {
			return fmt.Errorf("write predictions for %s: %w", driver, err)
		}
		verdicts, err := invokeEvaluator(ctx, cfg, driver, predsPath, workDir,
			fmt.Sprintf("%s-%s", cfg.RunID, driver), false)
		if err != nil {
			return fmt.Errorf("evaluator %s: %w", driver, err)
		}
		// Corrupt-image hygiene: instances the evaluator could not grade
		// (ErrEvaluatorError — usually a prebuilt image that won't start)
		// are retried once with a forced LOCAL image build. A genuine miss
		// stays a miss; a false negative from a bad registry image flips to
		// its real verdict.
		if retry := erroredRecs(recs, verdicts); len(retry) > 0 {
			mergeRebuildRetry(ctx, cfg, driver, workDir, retry, verdicts)
		}
		for _, rec := range recs {
			if v, ok := verdicts[rec.InstanceID]; ok {
				rec.Resolved = new(v.Resolved)
				rec.EvaluatorError = v.Reason()
			}
		}
	}
	return nil
}

// erroredRecs returns the records whose verdict is an un-gradeable
// evaluator error — the candidates for a forced-rebuild retry.
func erroredRecs(recs []*TaskResult, verdicts map[string]evaluator.Verdict) []*TaskResult {
	var out []*TaskResult
	for _, rec := range recs {
		if v, ok := verdicts[rec.InstanceID]; ok && errors.Is(v.Err, evaluator.ErrEvaluatorError) {
			out = append(out, rec)
		}
	}
	return out
}

// mergeRebuildRetry re-scores retry on a forced local image build and
// merges the fresh verdicts into verdicts in place. Best-effort: it logs
// and returns on any failure, leaving the original evaluator-error
// verdicts untouched rather than aborting the whole score.
func mergeRebuildRetry(ctx context.Context, cfg ScoreConfig, driver, workDir string, retry []*TaskResult, verdicts map[string]evaluator.Verdict) {
	slog.WarnContext(ctx, "scorer: retrying evaluator-error instances with forced local image rebuild",
		"driver", driver, "count", len(retry))
	predsPath := filepath.Join(workDir, fmt.Sprintf("predictions-%s-rebuild.json", driver))
	if err := writePredictions(predsPath, driver, retry); err != nil {
		slog.WarnContext(ctx, "scorer: rebuild-retry predictions write failed; keeping original verdicts",
			"driver", driver, "err", err)
		return
	}
	fresh, err := invokeEvaluator(ctx, cfg, driver, predsPath, workDir,
		fmt.Sprintf("%s-%s-rebuild", cfg.RunID, driver), true)
	if err != nil {
		slog.WarnContext(ctx, "scorer: rebuild-retry failed; keeping original verdicts",
			"driver", driver, "err", err)
		return
	}
	maps.Copy(verdicts, fresh)
}

func writePredictions(path, driver string, recs []*TaskResult) error {
	preds := make([]evaluator.Prediction, 0, len(recs))
	for _, rec := range recs {
		preds = append(preds, evaluator.Prediction{
			InstanceID:      rec.InstanceID,
			ModelPatch:      rec.Result.Diff,
			ModelNameOrPath: driver,
		})
	}
	return evaluator.WritePredictions(path, preds)
}

func invokeEvaluator(ctx context.Context, cfg ScoreConfig, driver, predsPath, workDir, runID string, forceRebuild bool) (map[string]evaluator.Verdict, error) {
	args := []string{
		"-m", "swebench.harness.run_evaluation",
		"--dataset_name", cfg.DatasetName,
		"--predictions_path", predsPath,
		"--max_workers", strconv.Itoa(cfg.MaxWorkers),
		"--run_id", runID,
	}
	if forceRebuild {
		// Build the eval image locally instead of pulling the (corrupt)
		// prebuilt one from the registry namespace. swebench rejects
		// force_rebuild together with a namespace, so clear it explicitly.
		args = append(args, "--force_rebuild", "True", "--namespace", "")
	}
	cmd := exec.CommandContext(ctx, cfg.Python, args...)
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("invoke: %w\nstderr: %s", err, stderr.String())
	}

	// The evaluator writes its summary to <model_name>.<run_id>.json
	// in workDir (and per-instance logs to logs/<run_id>/<instance>/).
	// model_name == the driver name we put in predictions; run_id is
	// the suffixed one we passed above.
	summaryPath := filepath.Join(workDir, fmt.Sprintf("%s.%s.json", driver, runID))
	return evaluator.ParseSummary(summaryPath)
}
