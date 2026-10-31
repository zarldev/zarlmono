// Package refine owns the evaluation-only, single-round harness refinement flow.
package refine

import (
	"context"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/runner"
)

const journalStarted = "started"

// ProbeResult keeps operator/scorer output separate from the improver projection.
type ProbeResult struct {
	Record   runner.TaskResult
	Evidence harness.EvidenceView
}

// Probe must run the supplied trial from the same original task state, settle
// its resources, and verify its evidence receipt before returning.
type Probe func(context.Context, harness.Trial) (ProbeResult, error)

// Improver consumes only the explicitly projected, score-blind evidence.
// Implementations return decisions validated for general/domain or composed labels.
type Improver interface {
	Propose(context.Context, harness.EvidenceView) (harness.Improvement, error)
	Compose(context.Context, []harness.EvidenceView) (harness.Improvement, error)
}

// Event is a journal record, emitted synchronously around every external stage.
// The recorder must persist it before returning; an error stops further work.
type Event struct {
	Stage   string                  `json:"stage"`
	State   string                  `json:"state"`
	Receipt harness.EvidenceReceipt `json:"receipt,omitzero"`
	// IncompleteArtifact is only an unverified filename, never a settled receipt.
	IncompleteArtifact string              `json:"incomplete_artifact,omitempty"`
	Improvement        harness.Improvement `json:"improvement,omitzero"`
	Skipped            []string            `json:"skipped,omitempty"`
	Error              string              `json:"error,omitempty"`
}

// Round retains every executed arm. Frozen permits later scoring; it is not an
// adoption or correctness decision. On error, available probe records remain.
type Round struct {
	Records  []runner.TaskResult
	Frozen   bool
	NoChange bool
}

// PlannedDrivers is the maximum stage topology, in execution order. No-change
// decisions may skip later stages; skipped drivers are journaled, never scored zero.
func PlannedDrivers() []string {
	return []string{"zarlcode-harness-baseline-r1", "zarlcode-harness-general-r1", "zarlcode-harness-domain-r1", "zarlcode-harness-composed-r1"}
}

// Run executes at most four serial probes and two improver invocations. It owns
// no goroutines and never invokes a scorer, retries, or promotes generated guidance.
func Run(ctx context.Context, probe Probe, improver Improver, record func(Event) error) (Round, error) {
	var round Round
	var views []harness.EvidenceView
	runProbe := func(trial harness.Trial) error {
		stage := trial.DriverName()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := record(Event{Stage: stage, State: journalStarted}); err != nil {
			return err
		}
		result, err := probe(ctx, trial)
		if result.Record.DriverName != "" {
			round.Records = append(round.Records, result.Record)
		}
		if err != nil {
			event := Event{Stage: stage, State: "failed", Error: err.Error(), Receipt: result.Record.Result.Evidence}
			if event.Receipt.File == "" {
				event.IncompleteArtifact = result.Record.Result.EvidenceFile
			}
			if journalErr := record(event); journalErr != nil {
				return errors.Join(err, fmt.Errorf("record failed probe: %w", journalErr))
			}
			return err
		}
		views = append(views, result.Evidence)
		return record(Event{Stage: stage, State: "completed", Receipt: result.Record.Result.Evidence})
	}
	finish := func(skipped []string) (Round, error) {
		if err := record(Event{Stage: "round", State: "frozen", Skipped: skipped}); err != nil {
			return round, err
		}
		round.Frozen = true
		round.NoChange = len(skipped) != 0
		return round, nil
	}
	if err := runProbe(harness.BaselineTrial()); err != nil {
		return round, fmt.Errorf("baseline: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return round, err
	}
	if err := record(Event{Stage: "propose", State: journalStarted}); err != nil {
		return round, err
	}
	proposal, err := improver.Propose(ctx, views[0])
	if journalErr := record(improvementEvent("propose", proposal, err)); journalErr != nil {
		return round, errors.Join(err, journalErr)
	}
	if err != nil {
		return round, fmt.Errorf("propose: %w", err)
	}
	if proposal.Decision.NoChange {
		return finish(PlannedDrivers()[1:])
	}
	for _, trial := range proposal.Decision.Experiment.Trials()[1:] {
		if err := runProbe(trial); err != nil {
			return round, fmt.Errorf("probe candidate: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return round, err
	}
	if err := record(Event{Stage: "compose", State: journalStarted}); err != nil {
		return round, err
	}
	composition, err := improver.Compose(ctx, views)
	if journalErr := record(improvementEvent("compose", composition, err)); journalErr != nil {
		return round, errors.Join(err, journalErr)
	}
	if err != nil {
		return round, fmt.Errorf("compose: %w", err)
	}
	if composition.Decision.NoChange {
		return finish(PlannedDrivers()[3:])
	}
	if err := runProbe(composition.Decision.Experiment.Trials()[1]); err != nil {
		return round, fmt.Errorf("probe composition: %w", err)
	}
	return finish(nil)
}

func improvementEvent(stage string, improvement harness.Improvement, err error) Event {
	event := Event{Stage: stage, State: "completed", Improvement: improvement}
	if err != nil {
		event.State = "failed"
		event.Error = err.Error()
	}
	return event
}
