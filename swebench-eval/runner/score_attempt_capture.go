package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/swebench-eval/internal/evaluator"
)

func invokeEvaluator(ctx context.Context, cfg ScoreConfig, driver, predsPath, workDir, runID string, rebuild bool) (map[string]evaluator.Verdict, error) {
	attempt := ScoreAttempt{
		ID: uuid.NewString(), Driver: driver, RunID: runID, Dataset: cfg.DatasetName,
		Python: cfg.Python, MaxWorkers: cfg.MaxWorkers, WorkDir: workDir,
		PredictionsPath: predsPath, SummaryPath: filepath.Join(workDir, fmt.Sprintf("%s.%s.json", driver, runID)),
		Rebuild: rebuild, StartedAt: time.Now(), Status: ScoreStatuses.RUNNING,
	}
	if cfg.OnScoreAttempt != nil {
		if err := cfg.OnScoreAttempt(attempt); err != nil {
			return nil, fmt.Errorf("persist scoring attempt start: %w", err)
		}
	}

	verdicts, invokeErr := executeEvaluator(ctx, cfg, driver, predsPath, workDir, runID, rebuild)
	attempt.EndedAt = new(time.Now())
	attempt.Status = ScoreStatuses.COMPLETED
	if invokeErr != nil {
		attempt.Status = ScoreStatuses.FAILED
		if ctx.Err() != nil {
			attempt.Status = ScoreStatuses.CANCELLED
		}
		attempt.Diagnostic = invokeErr.Error()
	}
	// Keep the exact summary even when parsing failed or the evaluator exited nonzero.
	if body, readErr := os.ReadFile(attempt.SummaryPath); readErr == nil {
		attempt.Summary = string(body)
	}
	if cfg.OnScoreAttempt != nil {
		if captureErr := cfg.OnScoreAttempt(attempt); captureErr != nil {
			invokeErr = errors.Join(invokeErr, fmt.Errorf("persist scoring attempt outcome: %w", captureErr))
		}
	}
	return verdicts, invokeErr
}
