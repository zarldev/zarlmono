package runner_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// TestRun_ToolTimeoutMarksAbandoned covers P2.2: when a tool blows its
// per-tool time budget with its goroutine still running (blockingTool
// ignores ctx), the ToolFailed event is marked Abandoned so a consumer
// can distinguish it from an ordinary failure.
func TestRun_ToolTimeoutMarksAbandoned(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{turns: [][]llm.CompletionChunk{
		{chunkToolCall("c1", "slow", `{}`), chunkDone()}, // calls the blocking tool
		{chunkText("done"), chunkDone()},                 // completes after the timeout failure
	}}
	reg := newRegistry(blockingTool{name: "slow", started: make(chan struct{})})
	sink := newRecordingSink()

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg),
		runner.WithSink(sink),
		runner.WithMaxIterations(3),
		runner.WithToolTimeout(50*time.Millisecond),
	)
	if res := r.Run(t.Context(), runner.TaskSpec{
		ID: taskscope.ID(uuid.NewString()), Prompt: "go",
	}); res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	fails := sink.toolFailedEvents()
	if len(fails) != 1 {
		t.Fatalf("ToolFailed events = %d, want 1", len(fails))
	}
	if !fails[0].Abandoned {
		t.Error("a tool abandoned past its time budget should be marked Abandoned")
	}
}

func (s *recordingSink) toolFailedEvents() []runner.ToolFailed {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]runner.ToolFailed(nil), s.toolFails...)
}
