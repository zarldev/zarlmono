package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type recordingToolOutputSink struct {
	records []runner.ToolOutput
}

func (s *recordingToolOutputSink) Record(_ context.Context, out runner.ToolOutput) {
	s.records = append(s.records, out)
}

func TestToolOutputSinkReceivesFullOutput(t *testing.T) {
	full := strings.Repeat("line\n", 500) // far past the truncator caps

	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("c1", "big", `{"n":500}`), runnertest.ChunkDone()},
		{runnertest.ChunkText("done"), runnertest.ChunkDone()},
	})
	reg := tools.NewRegistry(runnertest.Tool{Name: "big", Description: "big", Result: full})

	sink := &recordingToolOutputSink{}
	r := runner.New(client,
		runner.WithTools(reg),
		runner.WithSink(runner.NopSink{}),
		runner.WithResultTruncator(runner.DefaultTruncator{MaxBytes: 64, MaxLines: 3}),
		runner.WithToolOutputSink(sink),
		runner.WithMaxIterations(4),
	)

	res := r.Run(context.Background(), runner.TaskSpec{Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("run: %v", res.Err)
	}

	if len(sink.records) != 1 {
		t.Fatalf("records = %d, want 1", len(sink.records))
	}
	got := sink.records[0]
	if got.ToolCallID != "c1" || got.ToolName != "big" || got.Args != `{"n":500}` {
		t.Fatalf("record meta = %+v", got)
	}
	if got.Output != full {
		t.Fatalf("sink output length = %d, want %d (full, untruncated)", len(got.Output), len(full))
	}
}
