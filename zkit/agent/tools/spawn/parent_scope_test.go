package spawn_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestParentScopeOwnsChildBeyondDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		group := spawn.NewGroup()
		defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
		parentCtx, cancelParent := context.WithCancel(t.Context())
		defer cancelParent()
		scope, err := group.Bind(parentCtx, "parent")
		if err != nil {
			t.Fatal(err)
		}
		client := &cancelClient{started: make(chan struct{})}
		tool := spawn.NewAsync(runner.New(client), group)
		dispatchCtx, cancelDispatch := context.WithCancel(taskscope.WithID(parentCtx, "parent"))
		id := startScopedTask(t, dispatchCtx, tool)
		<-client.started
		cancelDispatch()
		synctest.Wait()
		if snapshot, err := group.Peek(id); err != nil || snapshot.State != spawn.AgentTaskStates.RUNNING {
			t.Fatalf("dispatch cancellation stopped child: %#v, %v", snapshot, err)
		}
		cancelParent()
		terminal, err := group.Wait(t.Context(), id)
		if err != nil || terminal.State != spawn.AgentTaskStates.CANCELLED {
			t.Fatalf("parent cancellation = %#v, %v", terminal, err)
		}
		if err := scope.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		// A copied value handle shares the owner's sealed record, not a copy of
		// mutable lifecycle state. Late dispatch cannot reopen it.
		copyScope := scope
		if err := copyScope.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		result, err := tool.Execute(taskscope.WithID(t.Context(), "parent"), tools.ToolCall{ID: "late", Arguments: tools.ToolParameters{"prompt": "late"}})
		if err != nil || result.Success || !errors.Is(result.Err, spawn.ErrParentClosed) {
			t.Fatalf("late admission = %#v, %v", result, err)
		}
	})
}

func TestParentDeadlineStopsChild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		group := spawn.NewGroup()
		defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
		parentCtx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := group.Bind(parentCtx, "parent"); err != nil {
			t.Fatal(err)
		}
		client := &cancelClient{started: make(chan struct{})}
		id := startScopedTask(t, taskscope.WithID(parentCtx, "parent"), spawn.NewAsync(runner.New(client), group))
		<-client.started
		terminal, err := group.Wait(t.Context(), id)
		if err != nil || terminal.State != spawn.AgentTaskStates.FAILED || !terminal.Result.TimedOut {
			t.Fatalf("parent deadline = %#v, %v", terminal, err)
		}
	})
}

func TestParentScopeConcurrentCloseJoinsDescendants(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		group := spawn.NewGroup()
		defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
		scope, err := group.Bind(t.Context(), "parent")
		if err != nil {
			t.Fatal(err)
		}
		childClient := &cancelClient{started: make(chan struct{})}
		child := startScopedTask(t, taskscope.WithID(t.Context(), "parent"), spawn.NewAsync(runner.New(childClient), group))
		<-childClient.started
		grandchildClient := &cancelClient{started: make(chan struct{})}
		grandchild := startScopedTask(t, taskscope.WithID(t.Context(), taskscope.ID(child)), spawn.NewAsync(runner.New(grandchildClient), group))
		<-grandchildClient.started
		var closers sync.WaitGroup
		for range 8 {
			closers.Go(func() {
				if err := scope.Close(t.Context()); err != nil {
					t.Error(err)
				}
			})
		}
		closers.Go(func() {
			if err := group.Close(t.Context()); err != nil {
				t.Error(err)
			}
		})
		closers.Wait()
		for _, id := range []spawn.TaskID{child, grandchild} {
			if snapshot, err := group.Peek(id); err != nil || snapshot.State != spawn.AgentTaskStates.CANCELLED {
				t.Fatalf("descendant not joined: %#v, %v", snapshot, err)
			}
		}
	})
}

func TestGroupValueWaitersSurviveObservationAndPruning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		group := spawn.NewGroup(spawn.WithMaxObserved(1))
		defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
		client := &blockingClient{started: make(chan struct{}), release: make(chan struct{})}
		id := startScopedTask(t, t.Context(), spawn.NewAsync(runner.New(client), group))
		<-client.started
		var waiters sync.WaitGroup
		for range 20 {
			waiters.Go(func() {
				snapshot, err := group.Await(t.Context(), id)
				if err != nil || !snapshot.Observed || snapshot.Result.Summary != "child summary" {
					t.Errorf("waiter = %#v, %v", snapshot, err)
				}
			})
		}
		synctest.Wait()
		close(client.release)
		second := startScopedTask(t, t.Context(), spawn.NewAsync(runner.New(&immediateClient{content: "second"}), group))
		if _, err := group.Await(t.Context(), second); err != nil {
			t.Fatal(err)
		}
		waiters.Wait()
		if got := group.List(); len(got) != 1 || got[0].ID != second {
			t.Fatalf("retention after waiters = %#v", got)
		}
	})
}

func startScopedTask(t *testing.T, ctx context.Context, tool *spawn.AsyncTool) spawn.TaskID {
	t.Helper()
	result, err := tool.Execute(ctx, tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "focused work"}})
	if err != nil || !result.Success {
		t.Fatalf("spawn = %#v, %v", result, err)
	}
	return spawn.TaskID(result.Data.(map[string]any)["task_id"].(string))
}
