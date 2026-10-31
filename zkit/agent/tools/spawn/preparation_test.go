package spawn_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type preparationPlanner struct {
	calls    atomic.Int32
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (p *preparationPlanner) Plan(ctx context.Context, _ spawn.SpawnPlanInput) (spawn.SpawnPlan, error) {
	p.calls.Add(1)
	close(p.started)
	<-ctx.Done()
	close(p.canceled)
	<-p.release
	return spawn.SpawnPlan{Mode: spawn.SpawnModeImplement}, nil
}

func TestRejectedSpawnDoesNotPlan(t *testing.T) {
	for _, reason := range []string{"full", "closed", "sealed", "canceled"} {
		t.Run(reason, func(t *testing.T) {
			group := spawn.NewGroup(spawn.WithMaxConcurrent(1))
			defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
			ctx := taskscope.WithID(t.Context(), "parent")
			if _, err := group.Bind(ctx, "parent"); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "full":
				blocked := &blockingClient{started: make(chan struct{}), release: make(chan struct{})}
				startScopedTask(t, ctx, spawn.NewAsync(runner.New(blocked), group))
				<-blocked.started
				defer close(blocked.release)
			case "closed":
				if err := group.Close(ctx); err != nil {
					t.Fatal(err)
				}
			case "sealed":
				if !group.Finish("parent") {
					t.Fatal("empty parent not sealed")
				}
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			planner := &recordingPlanner{plan: spawn.SpawnPlan{Mode: spawn.SpawnModeImplement}}
			tool := spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group, spawn.WithSpawnPlannerCandidates(planner, []spawn.AgentCandidate{{Name: "worker"}}))
			result, err := tool.Execute(ctx, tools.ToolCall{ID: "rejected", Arguments: tools.ToolParameters{"prompt": "work"}})
			if err != nil || result.Success {
				t.Fatalf("rejected spawn = %v, %v", result, err)
			}
			if planner.calls != 0 || planner.probeCalls != 0 {
				t.Fatalf("refused work planned: plan=%d probe=%d", planner.calls, planner.probeCalls)
			}
		})
	}
}

func TestPreparationIsOwnedAndBlocksFinish(t *testing.T) {
	for _, closeParent := range []bool{false, true} {
		name := "group"
		if closeParent {
			name = "parent"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				group := spawn.NewGroup(spawn.WithMaxConcurrent(1))
				ctx := taskscope.WithID(t.Context(), "parent")
				scope, err := group.Bind(ctx, "parent")
				if err != nil {
					t.Fatal(err)
				}
				planner := &preparationPlanner{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
				tool := spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group, spawn.WithSpawnPlannerCandidates(planner, []spawn.AgentCandidate{{Name: "worker"}}))
				done := make(chan struct{})
				var result *tools.ToolResult
				var executeErr error
				go func() {
					defer close(done)
					result, executeErr = tool.Execute(ctx, tools.ToolCall{ID: "preparing", Arguments: tools.ToolParameters{"prompt": "work"}})
				}()
				<-planner.started
				defer func() {
					close(planner.release)
					_ = group.Close(context.WithoutCancel(ctx))
					<-done
					if executeErr != nil || result.Success {
						t.Errorf("canceled preparation promoted child: %v, %v", result, executeErr)
					}
					if len(group.List()) != 0 {
						t.Error("canceled preparation published a child receipt")
					}
				}()
				ready := group.Ready("parent")
				if !ready.Outstanding || len(ready.Inputs) != 0 {
					t.Errorf("preparation readiness = %#v", ready)
				}
				if group.Finish("parent") {
					t.Error("Finish sealed an active preparation")
				}
				// A competing call must not enter this planner (which only permits one call).
				competing := spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group, spawn.WithSpawnPlannerCandidates(&recordingPlanner{}, []spawn.AgentCandidate{{Name: "worker"}}))
				if r, e := competing.Execute(ctx, tools.ToolCall{ID: "second", Arguments: tools.ToolParameters{"prompt": "work"}}); e != nil || r.Success {
					t.Errorf("preparation did not reserve capacity: %v, %v", r, e)
				}
				expired, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				var closeErr error
				if closeParent {
					closeErr = scope.Close(expired)
				} else {
					closeErr = group.Close(expired)
				}
				if !errors.Is(closeErr, context.DeadlineExceeded) {
					t.Errorf("close did not retain active preparation: %v", closeErr)
				}
				select {
				case <-planner.canceled:
				default:
					t.Error("preparation was not canceled")
				}
				select {
				case <-done:
					t.Error("close abandoned preparation")
				default:
				}
			})
		})
	}
}
