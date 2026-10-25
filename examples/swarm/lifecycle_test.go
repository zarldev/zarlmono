package main_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	experiment "github.com/zarldev/zarlmono/examples/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
)

type parkedSink struct {
	runner.NopSink
	onStart func()
	started atomic.Int32
	exited  atomic.Int32
}

func (s *parkedSink) OnConversationStarted(ctx context.Context, _ runner.ConversationStarted) {
	s.started.Add(1)
	s.onStart()
	<-ctx.Done()
	s.exited.Add(1)
}

func TestCancellationAndRuntimeBudgetDrainActiveWorkers(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "cancel after worker starts", err: context.Canceled},
		{name: "runtime budget with active workers", err: swarm.ErrBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				sink := &parkedSink{onStart: func() {}}
				if errors.Is(tc.err, context.Canceled) {
					sink.onStart = cancel
				}
				// Synctest advances the runtime deadline only once the active
				// callbacks are parked. No sleep or real-time scheduling race.
				report, err := experiment.RunExperiment(ctx, experiment.ExperimentLimits(), sink)
				if !errors.Is(err, tc.err) {
					t.Fatalf("got %v, want %v", err, tc.err)
				}
				if sink.started.Load() == 0 || sink.started.Load() != sink.exited.Load() {
					t.Fatalf("callbacks started=%d exited=%d", sink.started.Load(), sink.exited.Load())
				}
				if report.Complete || !report.ShutdownJoined || report.InitiallyIdle {
					t.Fatalf("active-worker failure reported incorrectly: %+v", report)
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
