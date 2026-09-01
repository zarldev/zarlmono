package spawn_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestAsyncToolReturnsReceiptBeforeChildCompletes(t *testing.T) {
	client := &blockingClient{started: make(chan struct{}), release: make(chan struct{})}
	child := runner.New(client, runner.WithSink(runner.NopSink{}))
	group := spawn.NewGroup()
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	tool := spawn.NewAsync(spawn.New(child), group)

	res, err := tool.Execute(t.Context(), tools.ToolCall{ID: "spawn-call", Arguments: tools.ToolParameters{"prompt": "investigate"}})
	if err != nil || res == nil || !res.Success {
		t.Fatalf("agent_spawn = (%#v, %v), want receipt", res, err)
	}
	<-client.started
	id := spawn.TaskID(res.Data.(map[string]any)["task_id"].(string))
	if got := group.List(); len(got) != 1 || got[0].State != spawn.AgentTaskStates.RUNNING {
		t.Fatalf("tasks = %#v, want one RUNNING task", got)
	}

	close(client.release)
	snapshot, err := group.Await(t.Context(), id)
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if snapshot.State != spawn.AgentTaskStates.COMPLETED || snapshot.Result.Summary != "child summary" || !snapshot.Observed {
		t.Fatalf("terminal snapshot = %#v", snapshot)
	}
	if outstanding := group.Outstanding(); len(outstanding) != 0 {
		t.Fatalf("outstanding after await = %#v", outstanding)
	}
}

func TestAsyncChildrenPublishWorkspaceWaitAndSerializeOverlappingTools(t *testing.T) {
	source := &spawnBlockingWriteSource{entered: make(chan spawnWriteEntry, 2)}
	coordinator := tools.NewWorkspaceCoordinator()
	waitStarted := make(chan struct{}, 1)
	sink := &workspaceWaitSink{Sink: runnertest.NewSink(), started: waitStarted}
	child := runner.New(
		&spawnWriteClient{},
		runner.WithTools(coderunner.CoordinateWorkspace(source, coordinator)),
		runner.WithSink(sink),
		runner.WithMaxIterations(3),
	)
	group := spawn.NewGroup()
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	start := spawn.NewAsync(spawn.New(child), group)

	first := startSpawnWriteTask(t, start, "first", "zkit/a.go")
	firstEntry := receiveSpawnWriteEntry(t, source.entered)
	second := startSpawnWriteTask(t, start, "second", "zkit/a.go")
	<-waitStarted
	select {
	case entry := <-source.entered:
		t.Fatalf("overlapping child entered before release: %#v", entry)
	default:
	}

	close(firstEntry.release)
	secondEntry := receiveSpawnWriteEntry(t, source.entered)
	close(secondEntry.release)
	for _, id := range []spawn.TaskID{first, second} {
		snapshot, err := group.Await(t.Context(), id)
		if err != nil || snapshot.State != spawn.AgentTaskStates.COMPLETED {
			t.Fatalf("await %s = (%#v, %v)", id, snapshot, err)
		}
	}
	started, ok := sink.FirstWorkspaceWaitStarted()
	if !ok || started.ToolName != "write_test" || len(started.Paths) != 1 || started.Paths[0] != "zkit/a.go" {
		t.Fatalf("wait started = %#v", started)
	}
	ended, ok := sink.FirstWorkspaceWaitEnded()
	if !ok || ended.Outcome != tools.WorkspaceWaitOutcomes.WORKSPACEWAITACQUIRED {
		t.Fatalf("wait ended = %#v", ended)
	}
}

func TestListAgentTasksDoesNotConsumeOrExposeTerminalSummary(t *testing.T) {
	group := spawn.NewGroup()
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	start := spawn.NewAsync(spawn.New(runner.New(&immediateClient{content: "secret summary"}, runner.WithSink(runner.NopSink{}))), group)
	id := startImmediateTask(t, start, "spawn")
	if _, err := group.Wait(t.Context(), id); err != nil {
		t.Fatalf("wait terminal: %v", err)
	}

	listed, err := spawn.NewList(group).Execute(t.Context(), tools.ToolCall{ID: "list"})
	if err != nil || listed == nil || !listed.Success {
		t.Fatalf("list = (%#v, %v)", listed, err)
	}
	tasks := listed.Data.(map[string]any)["tasks"].([]map[string]any)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %#v", tasks)
	}
	if _, exposed := tasks[0]["summary"]; exposed {
		t.Fatalf("list exposed terminal summary: %#v", tasks[0])
	}
	if _, exposed := tasks[0]["error"]; exposed {
		t.Fatalf("list exposed terminal error: %#v", tasks[0])
	}
	if observed, _ := tasks[0]["observed"].(bool); observed {
		t.Fatalf("list marked terminal observed: %#v", tasks[0])
	}
	if got := group.Outstanding(); len(got) != 1 || got[0].ID != id {
		t.Fatalf("outstanding after list = %#v", got)
	}

	status, err := spawn.NewStatus(group).Execute(t.Context(), tools.ToolCall{ID: "status", Arguments: tools.ToolParameters{"task_id": string(id)}})
	if err != nil || status == nil || !status.Success {
		t.Fatalf("status = (%#v, %v)", status, err)
	}
	if summary := status.Data.(map[string]any)["summary"]; summary != "secret summary" {
		t.Fatalf("status summary = %#v", summary)
	}
	if got := group.Outstanding(); len(got) != 0 {
		t.Fatalf("outstanding after status = %#v", got)
	}
}

func TestLifecycleToolsFailSafelyWithoutGroup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		tool tools.Tool
	}{
		{name: "await", tool: spawn.NewAwait(nil)},
		{name: "status", tool: spawn.NewStatus(nil)},
		{name: "stop", tool: spawn.NewStop(nil)},
		{name: "list", tool: spawn.NewList(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := tc.tool.Execute(t.Context(), tools.ToolCall{ID: "call", Arguments: tools.ToolParameters{"task_id": "task"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.FATAL {
				t.Fatalf("result = %#v, want fatal configuration failure", result)
			}
		})
	}
}

func TestAsyncToolEnforcesConcurrentChildCap(t *testing.T) {
	client := &blockingClient{started: make(chan struct{}), release: make(chan struct{})}
	child := runner.New(client, runner.WithSink(runner.NopSink{}))
	group := spawn.NewGroup(spawn.WithMaxConcurrent(1))
	t.Cleanup(func() {
		close(client.release)
		_ = group.Close(t.Context())
	})
	tool := spawn.NewAsync(spawn.New(child), group)

	first, err := tool.Execute(t.Context(), tools.ToolCall{ID: "first", Arguments: tools.ToolParameters{"prompt": "wait"}})
	if err != nil || !first.Success {
		t.Fatalf("first spawn = (%#v, %v)", first, err)
	}
	<-client.started
	second, err := tool.Execute(t.Context(), tools.ToolCall{ID: "second", Arguments: tools.ToolParameters{"prompt": "also wait"}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Success || second.Err == nil || second.Err.Kind != tools.Kinds.BUDGET {
		t.Fatalf("second spawn = %#v, want concurrency budget failure", second)
	}
}

func TestAsyncToolRejectsNegativeMaxIterationsBeforeAdmission(t *testing.T) {
	t.Parallel()
	group := spawn.NewGroup()
	tool := spawn.NewAsync(spawn.New(nil), group)
	result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "work", "max_iterations": -1}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.VALIDATION {
		t.Fatalf("result = %#v, want validation failure", result)
	}
	if got := group.List(); len(got) != 0 {
		t.Fatalf("invalid spawn admitted tasks: %#v", got)
	}
}

func TestAsyncToolContainsChildPanicAndReleasesCapacity(t *testing.T) {
	child := runner.New(panicClient{}, runner.WithSink(runner.NopSink{}))
	group := spawn.NewGroup(spawn.WithMaxConcurrent(1))
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	tool := spawn.NewAsync(spawn.New(child), group)

	first, err := tool.Execute(t.Context(), tools.ToolCall{ID: "first", Arguments: tools.ToolParameters{"prompt": "panic"}})
	if err != nil || first == nil || !first.Success {
		t.Fatalf("first spawn = (%#v, %v), want receipt", first, err)
	}
	firstID := spawn.TaskID(first.Data.(map[string]any)["task_id"].(string))
	snapshot, err := group.Await(t.Context(), firstID)
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if snapshot.State != spawn.AgentTaskStates.FAILED || snapshot.Result.Reason != runner.TerminalError {
		t.Fatalf("panic snapshot = %#v, want FAILED/error", snapshot)
	}
	if !strings.Contains(snapshot.Result.Error, "sub-agent panic: provider exploded") {
		t.Fatalf("panic error = %q", snapshot.Result.Error)
	}

	second, err := tool.Execute(t.Context(), tools.ToolCall{ID: "second", Arguments: tools.ToolParameters{"prompt": "panic again"}})
	if err != nil || second == nil || !second.Success {
		t.Fatalf("second spawn = (%#v, %v), want released capacity", second, err)
	}
}

func TestGroupEvictsOldestObservedTerminalTasks(t *testing.T) {
	group := spawn.NewGroup(spawn.WithMaxObserved(2))
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	tool := spawn.NewAsync(spawn.New(runner.New(&immediateClient{content: "done"}, runner.WithSink(runner.NopSink{}))), group)

	ids := make([]spawn.TaskID, 0, 3)
	for i := range 3 {
		result, err := tool.Execute(t.Context(), tools.ToolCall{ID: tools.ToolCallID(fmt.Sprintf("spawn-%d", i)), Arguments: tools.ToolParameters{"prompt": "finish"}})
		if err != nil || result == nil || !result.Success {
			t.Fatalf("spawn %d = (%#v, %v)", i, result, err)
		}
		id := spawn.TaskID(result.Data.(map[string]any)["task_id"].(string))
		ids = append(ids, id)
		if _, err := group.Await(t.Context(), id); err != nil {
			t.Fatalf("await %d: %v", i, err)
		}
	}

	if _, err := group.Peek(ids[0]); !errors.Is(err, spawn.ErrTaskNotFound) {
		t.Fatalf("oldest observed Peek error = %v, want ErrTaskNotFound", err)
	}
	listed := group.List()
	if len(listed) != 2 || listed[0].ID != ids[1] || listed[1].ID != ids[2] {
		t.Fatalf("retained tasks = %#v, want latest two observed tasks", listed)
	}
}

func TestGroupNeverEvictsUnobservedTerminalTasks(t *testing.T) {
	group := spawn.NewGroup(spawn.WithMaxObserved(1))
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	tool := spawn.NewAsync(spawn.New(runner.New(&immediateClient{content: "done"}, runner.WithSink(runner.NopSink{}))), group)

	unobserved := startImmediateTask(t, tool, "unobserved")
	firstObserved := startImmediateTask(t, tool, "observed-1")
	if _, err := group.Await(t.Context(), firstObserved); err != nil {
		t.Fatal(err)
	}
	latestObserved := startImmediateTask(t, tool, "observed-2")
	if _, err := group.Await(t.Context(), latestObserved); err != nil {
		t.Fatal(err)
	}
	if err := group.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := group.Peek(unobserved); err != nil {
		t.Fatalf("unobserved terminal was evicted: %v", err)
	}
	if _, err := group.Peek(firstObserved); !errors.Is(err, spawn.ErrTaskNotFound) {
		t.Fatalf("old observed Peek error = %v, want ErrTaskNotFound", err)
	}
	if got := group.Outstanding(); len(got) != 1 || got[0].ID != unobserved {
		t.Fatalf("outstanding = %#v, want unobserved terminal", got)
	}
}

func startImmediateTask(t *testing.T, tool tools.Tool, callID string) spawn.TaskID {
	t.Helper()
	result, err := tool.Execute(t.Context(), tools.ToolCall{ID: tools.ToolCallID(callID), Arguments: tools.ToolParameters{"prompt": "finish"}})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("spawn %s = (%#v, %v)", callID, result, err)
	}
	return spawn.TaskID(result.Data.(map[string]any)["task_id"].(string))
}

func TestGroupCloseCancelsAndJoinsChild(t *testing.T) {
	client := &cancelClient{started: make(chan struct{})}
	child := runner.New(client, runner.WithSink(runner.NopSink{}))
	group := spawn.NewGroup()
	tool := spawn.NewAsync(spawn.New(child), group)
	res, err := tool.Execute(t.Context(), tools.ToolCall{ID: "spawn-call", Arguments: tools.ToolParameters{"prompt": "wait"}})
	if err != nil || res == nil || !res.Success {
		t.Fatalf("agent_spawn = (%#v, %v), want receipt", res, err)
	}
	<-client.started
	id := spawn.TaskID(res.Data.(map[string]any)["task_id"].(string))
	if err := group.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	snapshot, err := group.Snapshot(id)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.State != spawn.AgentTaskStates.CANCELLED {
		t.Fatalf("state = %s, want CANCELLED", snapshot.State)
	}
}

func TestAgentAwaitClassifiesCancelledTaskAsTransient(t *testing.T) {
	client := &cancelClient{started: make(chan struct{})}
	group := spawn.NewGroup()
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	start := spawn.NewAsync(spawn.New(runner.New(client, runner.WithSink(runner.NopSink{}))), group)
	spawned, err := start.Execute(t.Context(), tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "wait"}})
	if err != nil || !spawned.Success {
		t.Fatalf("spawn = (%#v, %v)", spawned, err)
	}
	<-client.started
	id := spawned.Data.(map[string]any)["task_id"].(string)
	if _, err := spawn.NewStop(group).Execute(t.Context(), tools.ToolCall{ID: "stop", Arguments: tools.ToolParameters{"task_id": id}}); err != nil {
		t.Fatal(err)
	}

	result, err := spawn.NewAwait(group).Execute(t.Context(), tools.ToolCall{ID: "await", Arguments: tools.ToolParameters{"task_id": id}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.TRANSIENT {
		t.Fatalf("cancelled await = %#v, want transient failure", result)
	}
}

func TestAgentAwaitClassifiesChildErrorAsFatal(t *testing.T) {
	group := spawn.NewGroup()
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	start := spawn.NewAsync(spawn.New(runner.New(errorClient{}, runner.WithSink(runner.NopSink{}))), group)
	spawned, err := start.Execute(t.Context(), tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "fail"}})
	if err != nil || !spawned.Success {
		t.Fatalf("spawn = (%#v, %v)", spawned, err)
	}
	id := spawned.Data.(map[string]any)["task_id"].(string)
	result, err := spawn.NewAwait(group).Execute(t.Context(), tools.ToolCall{ID: "await", Arguments: tools.ToolParameters{"task_id": id}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.FATAL {
		t.Fatalf("errored await = %#v, want fatal failure", result)
	}
}

func TestAgentAwaitClassifiesIterationExhaustionAsBudget(t *testing.T) {
	group := spawn.NewGroup()
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	provider := &scriptedProvider{turns: [][]llm.CompletionChunk{{toolCallChunk("probe", "missing")}}}
	child := runner.New(runner.ClientFromProvider(provider), runner.WithSink(runner.NopSink{}), runner.WithMaxIterations(1))
	start := spawn.NewAsync(spawn.New(child), group)
	spawned, err := start.Execute(t.Context(), tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "keep working"}})
	if err != nil || !spawned.Success {
		t.Fatalf("spawn = (%#v, %v)", spawned, err)
	}
	id := spawned.Data.(map[string]any)["task_id"].(string)
	result, err := spawn.NewAwait(group).Execute(t.Context(), tools.ToolCall{ID: "await", Arguments: tools.ToolParameters{"task_id": id}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.BUDGET {
		t.Fatalf("exhausted await = %#v, want budget failure", result)
	}
}

func TestGroupMaxRuntimeCancelsChildAndReportsBudget(t *testing.T) {
	client := &cancelClient{started: make(chan struct{})}
	group := spawn.NewGroup(spawn.WithMaxRuntime(20 * time.Millisecond))
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	start := spawn.NewAsync(spawn.New(runner.New(client, runner.WithSink(runner.NopSink{}))), group)
	spawned, err := start.Execute(t.Context(), tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "wait"}})
	if err != nil || !spawned.Success {
		t.Fatalf("spawn = (%#v, %v)", spawned, err)
	}
	<-client.started
	id := spawned.Data.(map[string]any)["task_id"].(string)
	result, err := spawn.NewAwait(group).Execute(t.Context(), tools.ToolCall{ID: "await", Arguments: tools.ToolParameters{"task_id": id}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.BUDGET {
		t.Fatalf("timed-out await = %#v, want budget failure", result)
	}
	data := result.Data.(map[string]any)
	if timedOut, _ := data["timed_out"].(bool); !timedOut {
		t.Fatalf("timed_out = %#v, want true", data["timed_out"])
	}
}

func TestAgentToolNamesFollowResourceVerbGrammar(t *testing.T) {
	group := spawn.NewGroup()
	toolsToCheck := []tools.Tool{spawn.NewAsync(spawn.New(nil), group), spawn.NewAwait(group), spawn.NewStatus(group), spawn.NewStop(group), spawn.NewList(group)}
	want := []tools.ToolName{spawn.ToolNameAgentSpawn, spawn.ToolNameAgentAwait, spawn.ToolNameAgentStatus, spawn.ToolNameAgentStop, spawn.ToolNameListAgentTasks}
	for i, tool := range toolsToCheck {
		if got := tool.Definition().Name; got != want[i] {
			t.Errorf("tool %d name = %q, want %q", i, got, want[i])
		}
	}
}

func TestAgentAwaitCanRecoverSingleTaskIDAndTimesOutWithoutStoppingTask(t *testing.T) {
	client := &blockingClient{started: make(chan struct{}), release: make(chan struct{})}
	child := runner.New(client, runner.WithSink(runner.NopSink{}))
	group := spawn.NewGroup()
	t.Cleanup(func() {
		close(client.release)
		_ = group.Close(t.Context())
	})
	start := spawn.NewAsync(spawn.New(child), group)
	res, err := start.Execute(t.Context(), tools.ToolCall{ID: "spawn-call", Arguments: tools.ToolParameters{"prompt": "wait"}})
	if err != nil || res == nil || !res.Success {
		t.Fatalf("agent_spawn = (%#v, %v), want receipt", res, err)
	}
	<-client.started

	await := spawn.NewAwait(group, spawn.WithAwaitTimeout(time.Millisecond))
	waited, err := await.Execute(t.Context(), tools.ToolCall{ID: "await-call", Arguments: tools.ToolParameters{}})
	if err != nil || waited == nil || !waited.Success {
		t.Fatalf("agent_await = (%#v, %v), want running snapshot", waited, err)
	}
	data := waited.Data.(map[string]any)
	if data["status"] != spawn.AgentTaskStates.RUNNING.String() || data["timed_out"] != true {
		t.Fatalf("agent_await data = %#v, want timed-out RUNNING snapshot", data)
	}
}

func TestAgentAwaitRejectsInvalidRequestedTimeout(t *testing.T) {
	group := spawn.NewGroup()
	await := spawn.NewAwait(group, spawn.WithAwaitMaxTimeout(2*time.Second))
	for name, seconds := range map[string]int{
		"negative":           -1,
		"above host maximum": 3,
	} {
		t.Run(name, func(t *testing.T) {
			result, err := await.Execute(t.Context(), tools.ToolCall{ID: "await", Arguments: tools.ToolParameters{"task_id": "missing", "timeout_seconds": seconds}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.VALIDATION {
				t.Fatalf("result = %#v, want validation failure", result)
			}
		})
	}
}

func TestAgentAwaitInfersSoleRunningTaskAmongCompletedTasks(t *testing.T) {
	group := spawn.NewGroup()
	completedRunner := runner.New(&immediateClient{content: "done"}, runner.WithSink(runner.NopSink{}))
	completed, err := spawn.NewAsync(spawn.New(completedRunner), group).Execute(t.Context(), tools.ToolCall{ID: "completed", Arguments: tools.ToolParameters{"prompt": "finish"}})
	if err != nil || !completed.Success {
		t.Fatalf("completed spawn = (%#v, %v)", completed, err)
	}
	completedID := spawn.TaskID(completed.Data.(map[string]any)["task_id"].(string))
	if _, err := group.Await(t.Context(), completedID); err != nil {
		t.Fatal(err)
	}

	blocking := &blockingClient{started: make(chan struct{}), release: make(chan struct{})}
	runningRunner := runner.New(blocking, runner.WithSink(runner.NopSink{}))
	running, err := spawn.NewAsync(spawn.New(runningRunner), group).Execute(t.Context(), tools.ToolCall{ID: "running", Arguments: tools.ToolParameters{"prompt": "wait"}})
	if err != nil || !running.Success {
		t.Fatalf("running spawn = (%#v, %v)", running, err)
	}
	<-blocking.started
	t.Cleanup(func() {
		close(blocking.release)
		_ = group.Close(t.Context())
	})

	await := spawn.NewAwait(group, spawn.WithAwaitTimeout(time.Millisecond))
	result, err := await.Execute(t.Context(), tools.ToolCall{ID: "await", Arguments: tools.ToolParameters{}})
	if err != nil || !result.Success {
		t.Fatalf("agent_await = (%#v, %v)", result, err)
	}
	data := result.Data.(map[string]any)
	if data["task_id"] != running.Data.(map[string]any)["task_id"] || data["timed_out"] != true {
		t.Fatalf("agent_await data = %#v, want sole running task", data)
	}
}

func TestAgentAwaitDoesNotTreatParentDeadlineAsPollingTimeout(t *testing.T) {
	client := &blockingClient{started: make(chan struct{}), release: make(chan struct{})}
	child := runner.New(client, runner.WithSink(runner.NopSink{}))
	group := spawn.NewGroup()
	start := spawn.NewAsync(spawn.New(child), group)
	res, err := start.Execute(t.Context(), tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "wait"}})
	if err != nil || !res.Success {
		t.Fatalf("spawn = (%#v, %v)", res, err)
	}
	<-client.started
	t.Cleanup(func() {
		close(client.release)
		_ = group.Close(t.Context())
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	await := spawn.NewAwait(group, spawn.WithAwaitTimeout(time.Hour))
	result, err := await.Execute(ctx, tools.ToolCall{ID: "await", Arguments: tools.ToolParameters{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.TRANSIENT {
		t.Fatalf("agent_await = %#v, want parent deadline transient failure", result)
	}
}

func TestAgentAwaitRequiresRunningTaskWhenIDIsOmitted(t *testing.T) {
	group := spawn.NewGroup()
	child := runner.New(&immediateClient{content: "done"}, runner.WithSink(runner.NopSink{}))
	spawned, err := spawn.NewAsync(spawn.New(child), group).Execute(t.Context(), tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "finish"}})
	if err != nil || !spawned.Success {
		t.Fatalf("spawn = (%#v, %v)", spawned, err)
	}
	id := spawn.TaskID(spawned.Data.(map[string]any)["task_id"].(string))
	if _, err := group.Await(t.Context(), id); err != nil {
		t.Fatal(err)
	}

	result, err := spawn.NewAwait(group).Execute(t.Context(), tools.ToolCall{ID: "await", Arguments: tools.ToolParameters{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.VALIDATION {
		t.Fatalf("agent_await = %#v, want task_id validation failure", result)
	}
}

type immediateClient struct{ content string }

func (c *immediateClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: c.content}, nil)
	}
}

type spawnWriteClient struct{ calls atomic.Uint64 }

func (c *spawnWriteClient) Complete(_ context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		for _, message := range request.Messages {
			if message.Role == "tool" {
				yield(runnertest.ChunkText("done"), nil)
				return
			}
		}
		path := "zkit/a.go"
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "zarlcode/b.go") {
				path = "zarlcode/b.go"
			}
		}
		callID := fmt.Sprintf("write-%d", c.calls.Add(1))
		yield(runnertest.ChunkToolCall(callID, "write_test", fmt.Sprintf(`{"path":%q}`, path)), nil)
	}
}

type spawnWriteEntry struct {
	task    taskscope.ID
	path    string
	release chan struct{}
}

type spawnBlockingWriteSource struct{ entered chan spawnWriteEntry }

func (s *spawnBlockingWriteSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) { yield(spawnWriteTool{source: s}) }
}

func (s *spawnBlockingWriteSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	entry := spawnWriteEntry{task: taskscope.IDFrom(ctx), path: call.Arguments.String("path", ""), release: make(chan struct{})}
	select {
	case s.entered <- entry:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-entry.release:
		return tools.Success(call.ID, "written"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type spawnWriteTool struct{ source *spawnBlockingWriteSource }

func (t spawnWriteTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: "write_test", Description: "Test path-scoped write.", Mutates: true,
		WorkspaceAccess: tools.WorkspaceAccesses.WRITE,
		WorkspaceScope:  tools.WorkspaceScopeArgument("path"),
	}
}

func (t spawnWriteTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return t.source.Execute(ctx, call)
}

func startSpawnWriteTask(t *testing.T, tool tools.Tool, callID, path string) spawn.TaskID {
	t.Helper()
	result, err := tool.Execute(t.Context(), tools.ToolCall{ID: tools.ToolCallID(callID), Arguments: tools.ToolParameters{"prompt": "write " + path}})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("spawn %s = (%#v, %v)", callID, result, err)
	}
	return spawn.TaskID(result.Data.(map[string]any)["task_id"].(string))
}

func receiveSpawnWriteEntry(t *testing.T, entries <-chan spawnWriteEntry) spawnWriteEntry {
	t.Helper()
	select {
	case entry := <-entries:
		return entry
	case <-time.After(time.Second):
		t.Fatal("spawned write did not enter")
		return spawnWriteEntry{}
	}
}

type workspaceWaitSink struct {
	*runnertest.Sink
	started chan<- struct{}
}

func (s *workspaceWaitSink) OnWorkspaceWaitStarted(event runner.WorkspaceWaitStarted) {
	s.Sink.OnWorkspaceWaitStarted(event)
	s.started <- struct{}{}
}

type blockingClient struct {
	started chan struct{}
	release chan struct{}
}

func (c *blockingClient) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		select {
		case c.started <- struct{}{}:
		case <-ctx.Done():
			yield(llm.CompletionChunk{}, context.Cause(ctx))
			return
		}
		select {
		case <-c.release:
			yield(llm.CompletionChunk{Content: "child summary"}, nil)
		case <-ctx.Done():
			yield(llm.CompletionChunk{}, context.Cause(ctx))
		}
	}
}

type panicClient struct{}

func (panicClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(func(llm.CompletionChunk, error) bool) {
		panic("provider exploded")
	}
}

type cancelClient struct{ started chan struct{} }

func (c *cancelClient) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(c.started)
		<-ctx.Done()
		yield(llm.CompletionChunk{}, context.Cause(ctx))
	}
}

type errorClient struct{}

func (errorClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{}, errors.New("provider broke"))
	}
}
