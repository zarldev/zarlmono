package runner_test

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type silentProvider struct{}

func (silentProvider) Name() string { return "silent" }

func (silentProvider) Complete(ctx context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		<-ctx.Done()
	}, nil
}

func TestRun_StreamIdleCause(t *testing.T) {
	t.Parallel()

	r := runner.New(
		runner.ClientFromProvider(silentProvider{}),
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(1),
		runner.WithIterationTimeout(time.Second),
		runner.WithStreamIdleTimeout(20*time.Millisecond),
	)
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Cause != runner.TerminalCauseStreamIdle {
		t.Errorf("Cause = %q, want %q", res.Cause, runner.TerminalCauseStreamIdle)
	}
}
