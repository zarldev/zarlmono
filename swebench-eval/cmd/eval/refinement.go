package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/evalconfig"
	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/refine"
	"github.com/zarldev/zarlmono/swebench-eval/runner"
	"github.com/zarldev/zarlmono/swebench-eval/task"
)

func runRefinement(ctx context.Context, cfg evalconfig.Config, base runner.Config) (runner.Results, error) {
	results := runner.Results{Started: time.Now()}
	improver, err := harness.OpenImprover(ctx, harness.ImproverConfig{
		Provider: cfg.Zarlcode.Provider, Model: cfg.Zarlcode.Model, CodexEffort: cfg.Zarlcode.CodexEffort,
		StateDB: cfg.Zarlcode.StateDB, Timeout: cfg.Refinement.ImproverTimeout,
	})
	if err != nil {
		results.Ended = time.Now()
		return results, err
	}
	defer improver.Close()
	for _, spec := range base.Specs {
		round, err := refineTask(ctx, cfg, base, spec, improver)
		results.Records = append(results.Records, round.Records...)
		if err != nil {
			results.Ended = time.Now()
			return results, fmt.Errorf("refine task %s: %w", spec.InstanceID, err)
		}
	}
	results.Ended = time.Now()
	return results, nil
}

func refineTask(parent context.Context, cfg evalconfig.Config, base runner.Config, spec task.Spec, improver refine.Improver) (refine.Round, error) {
	ctx, cancel := context.WithTimeout(parent, 4*cfg.Execution.TaskTimeout+2*cfg.Refinement.ImproverTimeout)
	defer cancel()
	journal, err := os.CreateTemp(cfg.Persistence.EvidenceDir, "round-*.jsonl")
	if err != nil {
		return refine.Round{}, fmt.Errorf("create refinement journal: %w", err)
	}
	defer journal.Close()
	written := 0
	record := func(event refine.Event) error {
		data, err := json.Marshal(struct {
			Version int          `json:"version"`
			RunID   string       `json:"run_id"`
			TaskID  string       `json:"task_id"`
			At      time.Time    `json:"at"`
			Event   refine.Event `json:"event"`
		}{1, cfg.Persistence.RunID, spec.InstanceID, time.Now().UTC(), event})
		if err != nil {
			return fmt.Errorf("encode refinement journal: %w", err)
		}
		if written+len(data)+1 > 1<<20 {
			return errors.New("refinement journal exceeds 1 MiB")
		}
		if _, err = journal.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("write refinement journal: %w", err)
		}
		written += len(data) + 1
		if err = journal.Sync(); err != nil {
			return fmt.Errorf("sync refinement journal: %w", err)
		}
		return nil
	}
	probe := func(ctx context.Context, trial harness.Trial) (refine.ProbeResult, error) {
		driver := newZarlcodeDriver(cfg, harness.Ablation{}, trial)
		defer driver.Close()
		probeConfig := base
		probeConfig.Drivers = []harness.Driver{driver}
		probeConfig.Specs = []task.Spec{spec}
		probeConfig.TaskConcurrency = 1
		results, err := runner.Run(ctx, probeConfig)
		var outcome refine.ProbeResult
		if len(results.Records) == 1 {
			outcome.Record = results.Records[0]
		}
		if err != nil {
			return outcome, err
		}
		if len(results.Records) != 1 {
			return outcome, errors.New("probe did not produce exactly one result")
		}
		if outcome.Record.Result.Err != nil {
			return outcome, outcome.Record.Result.Err
		}
		outcome.Evidence, err = harness.ReadTrialEvidence(ctx, cfg.Persistence.EvidenceDir, outcome.Record.Result.Evidence)
		return outcome, err
	}
	round, err := refine.Run(ctx, probe, improver, record)
	return round, errors.Join(err, journal.Close())
}
