package engine_test

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestAgentAwareTurnQualityTracksRunningUnreadAndObservedTasks(t *testing.T) {
	client := &qualityBlockingClient{started: make(chan struct{}), release: make(chan struct{})}
	child := runner.New(client, runner.WithSink(runner.NopSink{}))
	group := spawn.NewGroup()
	t.Cleanup(func() { _ = group.Close(t.Context()) })
	launch := spawn.NewAsync(spawn.New(child), group)
	res, err := launch.Execute(t.Context(), tools.ToolCall{ID: "spawn", Arguments: tools.ToolParameters{"prompt": "work"}})
	if err != nil || res == nil || !res.Success {
		t.Fatalf("agent_spawn = (%#v, %v)", res, err)
	}
	<-client.started
	id := spawn.TaskID(res.Data.(map[string]any)["task_id"].(string))
	quality := engine.NewAgentAwareTurnQuality(nil, group)

	decision := quality.Inspect("done", nil)
	if !strings.Contains(decision.Correction, string(id)) {
		t.Fatalf("running correction = %q, want task id", decision.Correction)
	}

	close(client.release)
	for {
		tasks := group.List()
		if len(tasks) == 1 && tasks[0].State != spawn.AgentTaskStates.RUNNING {
			break
		}
	}
	decision = quality.Inspect("done", nil)
	if !strings.Contains(decision.Correction, string(id)) {
		t.Fatalf("unread correction = %q, want task id", decision.Correction)
	}
	if _, err := group.Await(t.Context(), id); err != nil {
		t.Fatalf("Await: %v", err)
	}
	if decision := quality.Inspect("done", nil); decision.Correction != "" {
		t.Fatalf("observed correction = %q, want empty", decision.Correction)
	}
}

type qualityBlockingClient struct {
	started chan struct{}
	release chan struct{}
}

func (c *qualityBlockingClient) Complete(ctx context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(c.started)
		select {
		case <-c.release:
			yield(llm.CompletionChunk{Content: "summary", Done: true}, nil)
		case <-ctx.Done():
		}
	}, nil
}
