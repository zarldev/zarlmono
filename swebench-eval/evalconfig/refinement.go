package evalconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	statedb "github.com/zarldev/zarlmono/zkit/db"
)

// RefinementConfig enables one serial, bounded refinement round per selected task.
type RefinementConfig struct {
	Enabled         bool
	ImproverTimeout time.Duration
}

func validateRefinement(cfg Config, experimentPath string) error {
	invalid := func(reason string) error { return fmt.Errorf("%w: refinement: %s", harness.ErrExperiment, reason) }
	if experimentPath != "" || strings.TrimSpace(cfg.Input.Drivers) != "zarlcode" || cfg.Input.AblationSpec != "" || cfg.Zarlcode.VerifiedAttempts > 1 || cfg.Zarlcode.VerifiedAttempts < 0 || cfg.Zarlcode.ThreadTranscript {
		return invalid("requires only zarlcode and no static experiment, ablation, or verified re-drive")
	}
	if cfg.Persistence.EvidenceDir == "" || strings.TrimSpace(cfg.Zarlcode.Provider) == "" || strings.TrimSpace(cfg.Zarlcode.Model) == "" || cfg.Zarlcode.StateDB == "" {
		return invalid("require explicit evidence directory, provider, model, and isolated --state-db")
	}
	if cfg.Zarlcode.EnvFile != "" {
		return invalid("environment-file loading is not supported; use isolated stored provider configuration")
	}
	if cfg.Input.Sample < 1 || cfg.Input.Sample > 10 {
		return invalid("require an explicit --sample between 1 and 10 for this bounded pilot mode")
	}
	if cfg.Execution.Concurrency != 1 || cfg.Execution.TaskTimeout <= 0 || cfg.Execution.TaskTimeout > 10*time.Minute || cfg.Zarlcode.MaxIter < 1 || cfg.Zarlcode.MaxIter > 64 || cfg.Refinement.ImproverTimeout <= 0 || cfg.Refinement.ImproverTimeout > 2*time.Minute {
		return invalid("require concurrency 1, 1–64 iterations, positive task timeout <=10m and improver timeout <=2m")
	}
	defaultState, err := statedb.DefaultPath()
	if err != nil {
		return fmt.Errorf("resolve credential home: %w", err)
	}
	state, err := filepath.Abs(cfg.Zarlcode.StateDB)
	if err != nil {
		return fmt.Errorf("resolve isolated state: %w", err)
	}
	if state == defaultState {
		return invalid("the default session database is not isolated evaluation state")
	}
	current, currentErr := os.Stat(state)
	original, originalErr := os.Stat(defaultState)
	if currentErr == nil && originalErr == nil && os.SameFile(current, original) {
		return invalid("state-db aliases the default session database")
	}
	return nil
}
