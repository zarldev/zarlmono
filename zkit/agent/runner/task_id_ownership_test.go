package runner_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type taskIDBlockingClient struct {
	started chan struct{}
	release chan struct{}
}

func (c *taskIDBlockingClient) Complete(ctx context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	select {
	case c.started <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-c.release:
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: "done", Done: true}, nil)
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRunRejectsConcurrentTaskIDReuseAndAllowsLaterGeneration(t *testing.T) {
	client := &taskIDBlockingClient{started: make(chan struct{}, 2), release: make(chan struct{})}
	r := runner.New(client, runner.WithSink(runner.NopSink{}))
	spec := runner.TaskSpec{ID: taskscope.ID("stable-id"), Prompt: "work"}
	firstDone := make(chan runner.TaskResult, 1)
	go func() { firstDone <- r.Run(t.Context(), spec) }()
	<-client.started

	second := r.Run(t.Context(), spec)
	if !errors.Is(second.Err, runner.ErrTaskIDActive) || second.Reason != runner.TerminalError {
		t.Fatalf("concurrent duplicate = %#v, want ErrTaskIDActive", second)
	}
	close(client.release)
	if first := <-firstDone; first.Reason != runner.TerminalCompleted {
		t.Fatalf("first result = %#v", first)
	}

	client.release = make(chan struct{})
	close(client.release)
	third := r.Run(t.Context(), spec)
	if third.Reason != runner.TerminalCompleted || third.Err != nil {
		t.Fatalf("later generation = %#v, want completed reuse", third)
	}
}
