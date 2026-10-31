package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	boardtools "github.com/zarldev/zarlmono/zkit/agent/tools/board"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ErrIncomplete means the swarm settled without completing the evidence chain.
var ErrIncomplete = errors.New("swarm experiment incomplete")

type contribution struct {
	role  swarm.AgentID
	topic string
	body  string
}

func scenario() []contribution {
	return []contribution{
		{role: "host", topic: "task", body: "Propose handling empty input in an average function. This is a synthetic protocol exercise, not a code change."},
		{role: "researcher", topic: "research", body: "Empty input reaches division by zero. Define an explicit empty-input error before calculating the average."},
		{role: "implementer", topic: "proposal", body: "Proposal: return an empty-input error when len(values) == 0; otherwise divide the sum by len(values). No source files were changed."},
		{role: "reviewer", topic: "review", body: "The proposal addresses the research finding. Live follow-up must test empty, singleton, and multi-value inputs. This scripted review is not a correctness verdict."},
	}
}

// ExperimentLimits bounds the scripted experiment to three workers, 24 turns,
// ten seconds, and eight retained findings. No provider reports token usage.
func ExperimentLimits() swarm.Limits {
	limits := swarm.DefaultLimits()
	limits.Members = 3
	limits.Workers = 3
	limits.Activations = 24
	limits.Runtime = 10 * time.Second
	limits.ReportedTokens = 0
	limits.Board = board.Limits{Posts: 8, Bytes: 16 << 10}
	return limits
}

// RunExperiment exercises real runner admission and board tools with scripted
// clients. Limits are trusted host policy, as for swarm.WithLimits. It first
// settles all agents, then publishes a trigger to exercise idle reactivation.
// Every return joins the swarm owner; no model, network, or filesystem tools run.
// The borrowed sink receives concurrent member events and must honor cancellation
// if its callbacks block. Use runner.NopSink{} when no live observation is needed.
func RunExperiment(ctx context.Context, limits swarm.Limits, sink runner.EventSink) (Report, error) {
	var report Report
	goal := swarm.New("research → proposal → review", swarm.WithLimits(limits))
	steps := scenario()
	members := make([]swarm.Member, 0, len(steps)-1)
	for index, step := range steps[1:] {
		member, addErr := goal.Add(swarm.Agent{
			ID: step.role,
			Task: runner.TaskSpec{AgentName: string(step.role),
				Prompt: "Act as " + string(step.role) + ". Inspect the board; contribute once when the prerequisite is available."},
			Execute: executeStep(steps[index], step, sink),
		})
		if addErr != nil {
			_ = goal.Close(ctx) // Run has not started; there are no workers to join.
			return report, fmt.Errorf("add %s: %w", step.role, addErr)
		}
		members = append(members, member)
	}

	done := make(chan error, 1)
	go func() { done <- goal.Run(ctx) }()
	err := runScenario(ctx, goal, steps, &report)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	closeErr := goal.Close(cleanup)
	// Close's deadline bounds its wait, not ownership. The scripted callbacks
	// and the borrowed sink must observe cancellation; always join.
	runErr := <-done
	if errors.Is(runErr, swarm.ErrClosed) {
		runErr = nil
	}
	reportErr := collectReport(context.WithoutCancel(ctx), goal, members[0], steps, &report)
	report.ShutdownJoined = true
	err = errors.Join(err, closeErr, runErr, reportErr)
	if err == nil && !report.Complete {
		err = ErrIncomplete
	}
	return report, err
}

func runScenario(ctx context.Context, goal *swarm.Swarm, steps []contribution, report *Report) error {
	if err := waitHealthy(ctx, goal, steps[1:]); err != nil {
		return err
	}
	report.InitiallyIdle = true
	draft, err := board.ParseDraft("Start the bounded experiment", steps[0].body, steps[0].topic, 0)
	if err != nil {
		return fmt.Errorf("parse trigger: %w", err)
	}
	if _, err := goal.Post(ctx, draft); err != nil {
		return fmt.Errorf("publish trigger: %w", err)
	}
	return waitHealthy(ctx, goal, steps[1:])
}

func executeStep(prerequisite, step contribution, sink runner.EventSink) swarm.Execute {
	return func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
		registry := tools.NewRegistry()
		if err := boardtools.Register(registry, activation.Member); err != nil {
			return runner.TaskResult{ID: activation.Spec.ID, Reason: runner.TerminalError, Err: err}
		}
		inputs := activation.Inputs(nil)
		client := &scriptedClient{prerequisite: prerequisite, step: step, runID: string(activation.Spec.ID)}
		r := runner.New(client, runner.WithTools(registry), runner.WithInputSource(inputs), runner.WithSink(sink), runner.WithMaxIterations(24))
		return inputs.Run(ctx, func(ctx context.Context) runner.TaskResult { return r.Run(ctx, activation.Spec) })
	}
}

func waitHealthy(ctx context.Context, goal *swarm.Swarm, steps []contribution) error {
	if err := goal.WaitIdle(ctx); err != nil {
		return fmt.Errorf("wait for swarm: %w", err)
	}
	for _, step := range steps {
		snapshot, err := goal.Snapshot(step.role)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", step.role, err)
		}
		if snapshot.State != swarm.States.IDLE || snapshot.LastError != nil {
			return errors.Join(fmt.Errorf("%w: %s is %s", ErrIncomplete, step.role, snapshot.State), snapshot.LastError)
		}
	}
	return nil
}
