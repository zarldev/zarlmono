package spawn_test

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
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
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.BUDGET {
		t.Fatalf("agent_await = %#v, want parent deadline budget failure", result)
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

func (c *immediateClient) Complete(context.Context, llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: c.content, Done: true}, nil)
	}, nil
}

type blockingClient struct {
	started chan struct{}
	release chan struct{}
}

func (c *blockingClient) Complete(ctx context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(c.started)
		select {
		case <-c.release:
			yield(llm.CompletionChunk{Content: "child summary", Done: true}, nil)
		case <-ctx.Done():
		}
	}, nil
}

type cancelClient struct{ started chan struct{} }

func (c *cancelClient) Complete(ctx context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(func(llm.CompletionChunk, error) bool) {
		close(c.started)
		<-ctx.Done()
	}, nil
}
