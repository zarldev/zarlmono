package runner_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRunAgentSpawnLetsParentContinueBeforeChildCompletes(t *testing.T) {
	client := &parentChildClient{
		childStarted: make(chan struct{}),
		childRelease: make(chan struct{}),
		parentSecond: make(chan struct{}),
	}
	reg := tools.NewRegistry()
	parent := runner.New(client, runner.WithTools(reg), runner.WithSink(runner.NopSink{}), runner.WithMaxIterations(4))
	group := spawn.NewGroup()
	coderunner.RegisterSpawnTools(reg, parent, group, 1, 2)
	t.Cleanup(func() { _ = group.Close(t.Context()) })

	done := make(chan runner.TaskResult, 1)
	go func() {
		done <- parent.Run(t.Context(), runner.TaskSpec{ID: "parent", Prompt: "delegate"})
	}()

	<-client.childStarted
	<-client.parentSecond
	close(client.childRelease)
	result := <-done
	if result.Err != nil || result.FinalContent != "parent continued" {
		t.Fatalf("parent result = %#v", result)
	}
}

type parentChildClient struct {
	mu           sync.Mutex
	parentCalls  int
	childStarted chan struct{}
	childRelease chan struct{}
	parentSecond chan struct{}
}

func (c *parentChildClient) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	if taskscope.DepthFrom(ctx) > 0 {
		return func(yield func(llm.CompletionChunk, error) bool) {
			close(c.childStarted)
			select {
			case <-c.childRelease:
				yield(llm.CompletionChunk{Content: "child done"}, nil)
			case <-ctx.Done():
			}
		}
	}

	c.mu.Lock()
	call := c.parentCalls
	c.parentCalls++
	c.mu.Unlock()
	switch call {
	case 0:
		return chunks(
			llm.CompletionChunk{ToolCalls: []llm.ToolCall{{ID: "spawn-1", Type: "function", Function: llm.ToolCallFunction{Name: "agent_spawn", Arguments: `{"prompt":"child work","mode":"explore"}`}}}},
			llm.CompletionChunk{},
		)
	case 1:
		close(c.parentSecond)
		return chunks(llm.CompletionChunk{Content: "parent continued"})
	default:
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{}, fmt.Errorf("unexpected parent completion %d with %d messages", call, len(req.Messages)))
		}
	}
}

func chunks(items ...llm.CompletionChunk) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}
	}
}
