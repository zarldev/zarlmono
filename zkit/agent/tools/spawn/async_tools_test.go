package spawn_test

import (
	"context"
	"iter"
	"testing"

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
