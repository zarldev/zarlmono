package runner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type failingHistorySink struct {
	cause error
	calls int
}

func (s *failingHistorySink) Record(context.Context, runner.ToolOutput) error {
	s.calls++
	return s.cause
}

func TestHistoryFailureStopsAfterCapturingSettledBatch(t *testing.T) {
	cause := errors.New("history unavailable")
	sink := &failingHistorySink{cause: cause}
	client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkToolCall("one", "capture", `{}`), runnertest.ChunkToolCall("two", "capture", `{}`)}, {runnertest.ChunkText("must not start")}})
	r := runner.New(client, runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "capture", Description: "capture", Result: "CANARY"})), runner.WithToolOutputSink(sink))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "capture"})
	if !errors.Is(result.Err, runner.ErrToolHistory) || !errors.Is(result.Err, cause) {
		t.Fatalf("capture failure: %v", result.Err)
	}
	if client.CallCount() != 1 || sink.calls != 2 {
		t.Fatalf("provider calls=%d captures=%d", client.CallCount(), sink.calls)
	}
}
