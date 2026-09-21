package spawn_test

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestGroupObservedLimitCanBeDisabled(t *testing.T) {
	for _, limit := range []int{0, -1} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			group := spawn.NewGroup(spawn.WithMaxObserved(1), spawn.WithMaxObserved(limit))
			defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
			tool := spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group)
			for i := range 3 {
				id := startImmediateTask(t, tool, strconv.Itoa(i))
				if _, err := group.Await(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			if got := len(group.List()); got != 3 {
				t.Fatalf("retained %d tasks, want 3", got)
			}
		})
	}
}

func TestAsyncRegisterSharesCallerOwnedGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &cancelClient{started: make(chan struct{})}
		group := spawn.NewGroup(spawn.WithMaxConcurrent(1), spawn.WithMaxConcurrent(0), spawn.WithMaxRuntime(time.Second), spawn.WithMaxRuntime(0))
		defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
		reg := tools.NewRegistry()
		launch := spawn.NewAsync(runner.New(client), group)
		launch.Register(reg, spawn.WithAwaitTimeout(time.Second))
		var names []tools.ToolName
		for tool := range reg.Tools(t.Context()) {
			names = append(names, tool.Definition().Name)
		}
		slices.Sort(names)
		want := []tools.ToolName{spawn.ToolNameAgentAwait, spawn.ToolNameAgentSpawn, spawn.ToolNameAgentStatus, spawn.ToolNameAgentStop, spawn.ToolNameListAgentTasks}
		slices.Sort(want)
		if !slices.Equal(names, want) {
			t.Fatalf("registered %v, want %v", names, want)
		}
		id := startImmediateTask(t, launch, "first")
		<-client.started
		// A later zero removes the concurrency cap as well as the runtime bound.
		second := spawn.NewAsync(runner.New(&immediateClient{content: "second"}), group)
		secondID := startImmediateTask(t, second, "second")
		if _, err := group.Await(t.Context(), secondID); err != nil {
			t.Fatal(err)
		}
		call := func(name tools.ToolName) *tools.ToolResult {
			t.Helper()
			result, err := reg.Execute(t.Context(), tools.ToolCall{ID: "lifecycle", ToolName: name, Arguments: tools.ToolParameters{"task_id": string(id)}})
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
		awaited := call(spawn.ToolNameAgentAwait)
		if !awaited.Success || awaited.Data.(map[string]any)["status"] != spawn.AgentTaskStates.RUNNING.String() {
			t.Fatalf("await = %#v", awaited)
		}
		if status := call(spawn.ToolNameAgentStatus); !status.Success {
			t.Fatalf("status = %#v", status)
		}
		if listed := call(spawn.ToolNameListAgentTasks); !listed.Success {
			t.Fatalf("list = %#v", listed)
		}
		stopped := call(spawn.ToolNameAgentStop)
		if stopped.Data.(map[string]any)["status"] != spawn.AgentTaskStates.CANCELLED.String() {
			t.Fatalf("stop = %#v", stopped)
		}
		snapshot, err := group.Peek(id)
		if err != nil || snapshot.State != spawn.AgentTaskStates.CANCELLED {
			t.Fatalf("caller group = (%#v, %v)", snapshot, err)
		}
	})
}
