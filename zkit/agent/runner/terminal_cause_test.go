package runner_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type silentProvider struct{ returned chan struct{} }

func (silentProvider) Name() string { return "silent" }

func (p silentProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(func(llm.CompletionChunk, error) bool) {
		<-ctx.Done()
		close(p.returned)
	}
}

func TestRun_StreamIdleCause(t *testing.T) {
	t.Parallel()

	returned := make(chan struct{})
	r := runner.New(
		runner.ClientFromProvider(silentProvider{returned: returned}),
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
	select {
	case <-returned:
	default:
		t.Error("Run returned before the idle-canceled provider exited")
	}
}
