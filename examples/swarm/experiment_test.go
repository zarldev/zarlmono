package main_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"

	experiment "github.com/zarldev/zarlmono/examples/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
)

func TestEvidenceChainAndIdleReactivation(t *testing.T) {
	for _, workers := range []int{1, 3} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limits := experiment.ExperimentLimits()
				limits.Workers = workers
				report, err := experiment.RunExperiment(t.Context(), limits, runner.NopSink{})
				if err != nil {
					t.Fatalf("run experiment: %v; report: %+v", err, report)
				}
				if !report.Complete || !report.InitiallyIdle || !report.ShutdownJoined || report.Handoffs != 3 || report.DuplicateContributions != 0 {
					t.Fatalf("incomplete protocol evidence: %+v", report)
				}
				if report.Mode != "scripted" || report.TokenUsage != "not reported by scripted clients; no live API calls" {
					t.Fatalf("misleading execution provenance: %+v", report)
				}
				if len(report.Members) != 3 || len(report.Findings) != 4 {
					t.Fatalf("members=%d findings=%d", len(report.Members), len(report.Findings))
				}
				for _, member := range report.Members {
					if member.Reactivations < 1 || member.Activations != member.Reactivations+1 || member.Responses < 4 {
						t.Errorf("member did not continue after initial idle: %+v", member)
					}
					if member.ReadCalls != 1 || member.PostCalls != 1 || member.InitialAssignments != 1 || member.Observations != 3 {
						t.Errorf("member lost history or repeated work: %+v", member)
					}
					if !member.Stopped || member.Pending != 0 {
						t.Errorf("member not drained: %+v", member)
					}
				}
				wantAuthors := []board.Author{"host", "agent/researcher", "agent/implementer", "agent/reviewer"}
				wantTopics := []string{"task", "research", "proposal", "review"}
				for index, finding := range report.Findings {
					if finding.ID != board.ID(index+1) || finding.ReplyTo != board.ID(index) ||
						finding.Author != wantAuthors[index] || finding.Topic != wantTopics[index] || finding.Body == "" {
						t.Errorf("broken handoff %d: %+v", index, finding)
					}
				}
				// Independent goals produce identical evidence despite fresh run IDs
				// and potentially different concurrent activation timing.
				again, err := experiment.RunExperiment(t.Context(), limits, runner.NopSink{})
				if err != nil {
					t.Fatalf("repeat experiment: %v; report: %+v", err, again)
				}
				if !slices.Equal(report.Findings, again.Findings) {
					t.Fatalf("non-reproducible evidence: %+v vs %+v", report.Findings, again.Findings)
				}
			})
		})
	}
}

func TestFailurePathsJoinTheGoal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
		limits swarm.Limits
		err    error
	}{
		{name: "cancelled caller", cancel: true, limits: experiment.ExperimentLimits(), err: context.Canceled},
		{name: "activation budget", limits: activationBudget(), err: swarm.ErrBudget},
		{name: "retention budget", limits: retentionBudget(), err: experiment.ErrIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tc.cancel {
					cancel()
				}
				report, err := experiment.RunExperiment(ctx, tc.limits, runner.NopSink{})
				if !errors.Is(err, tc.err) {
					t.Fatalf("got %v, want %v", err, tc.err)
				}
				if report.Complete || !report.ShutdownJoined || report.Handoffs == 3 {
					t.Fatalf("failure reported as success or left owner running: %+v", report)
				}
				for _, member := range report.Members {
					if !member.Stopped {
						t.Errorf("member survived shutdown: %+v", member)
					}
				}
			})
		})
	}
}

func activationBudget() swarm.Limits {
	limits := experiment.ExperimentLimits()
	limits.Activations = 3 // Initial assignments fit; the trigger cannot start another turn.
	return limits
}

func retentionBudget() swarm.Limits {
	limits := experiment.ExperimentLimits()
	limits.Board.Posts = 1 // Trigger fits; the research contribution must be rejected.
	return limits
}

func TestMembershipFailureStartsNoWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limits := experiment.ExperimentLimits()
		limits.Members = 2
		report, err := experiment.RunExperiment(t.Context(), limits, runner.NopSink{})
		if !errors.Is(err, swarm.ErrCapacity) {
			t.Fatalf("got %v, want capacity error", err)
		}
		if report.Complete || report.ShutdownJoined || len(report.Members) != 0 {
			t.Fatalf("reported a run after construction failed: %+v", report)
		}
	})
}
