package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zarldev/zarlmono/swebench-eval/evalconfig"
	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/refine"
)

func prepareExperiment(cfg *evalconfig.Config) error {
	trials := cfg.Experiment.Trials()
	if len(trials) == 0 && !cfg.Refinement.Enabled {
		return nil
	}
	//nolint:gosec // G703: the operator explicitly selects this CLI output directory.
	if err := os.MkdirAll(cfg.Persistence.EvidenceDir, 0o700); err != nil {
		return fmt.Errorf("create experiment artifacts: %w", err)
	}
	dir, err := os.MkdirTemp(cfg.Persistence.EvidenceDir, "experiment-")
	if err != nil {
		return fmt.Errorf("allocate experiment artifacts: %w", err)
	}
	cfg.Persistence.EvidenceDir, err = filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve experiment artifacts: %w", err)
	}
	// This sidecar deliberately has no dataset Spec or grading metadata.
	var planned []string
	if cfg.Refinement.Enabled {
		planned = refine.PlannedDrivers()
	}
	data, err := json.Marshal(struct {
		Version        int             `json:"version"`
		RunID          string          `json:"run_id"`
		Trials         []harness.Trial `json:"trials"`
		PlannedDrivers []string        `json:"planned_drivers,omitempty"`
	}{1, cfg.Persistence.RunID, trials, planned})
	if err != nil {
		return fmt.Errorf("encode experiment snapshot: %w", err)
	}
	//nolint:gosec // G703: fixed filename inside the private directory allocated by MkdirTemp above.
	if err := os.WriteFile(filepath.Join(dir, "experiment.json"), data, 0o600); err != nil {
		return fmt.Errorf("write experiment snapshot: %w", err)
	}
	fmt.Fprintf(os.Stderr, "harness evidence: %s\n", cfg.Persistence.EvidenceDir)
	return nil
}

func newZarlcodeDriver(cfg evalconfig.Config, arm harness.Ablation, trial harness.Trial) *harness.ZarlcodeDriver {
	return &harness.ZarlcodeDriver{
		Ablation: arm, Trial: trial, EvidenceDir: cfg.Persistence.EvidenceDir, ExperimentRunID: cfg.Persistence.RunID,
		EnvFile: cfg.Zarlcode.EnvFile, StateDB: cfg.Zarlcode.StateDB,
		MaxIter: cfg.Zarlcode.MaxIter, ToolConcurrency: cfg.Zarlcode.ToolConcurrency,
		ContextWindow: cfg.Zarlcode.ContextWindow, StreamIdle: cfg.Zarlcode.StreamIdle,
		IterationTimeout: cfg.Zarlcode.IterationTimeout, DeadlineGrace: cfg.Zarlcode.DeadlineGrace,
		Provider: cfg.Zarlcode.Provider, Model: cfg.Zarlcode.Model, CodexEffort: cfg.Zarlcode.CodexEffort,
		LlamacppResetURL: cfg.Zarlcode.LlamacppResetURL, AllowRemoteResetURL: cfg.Zarlcode.AllowRemoteResetURL,
		VerifiedAttempts: cfg.Zarlcode.VerifiedAttempts, VerifyDataset: cfg.Scoring.Dataset,
		VerifyPython: cfg.Scoring.Python, VerifyWorkers: cfg.Zarlcode.VerifyWorkers,
		VerifyWorkDir: cfg.Zarlcode.VerifyWorkDir, VerifyTimeout: cfg.Zarlcode.VerifyTimeout,
		ThreadTranscript: cfg.Zarlcode.ThreadTranscript, TranscriptDir: cfg.Zarlcode.TranscriptDir,
	}
}
