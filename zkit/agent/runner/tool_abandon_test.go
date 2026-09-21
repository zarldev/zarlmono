package runner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRun_ToolTimeoutJoinsExecution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		expired := make(chan struct{})
		tool := tools.New(tools.ToolSpec{Name: "slow", Description: "late result"}, func(ctx context.Context, _ map[string]any) (string, error) {
			<-ctx.Done()
			close(expired)
			<-release
			return "CANARY-late-result", nil
		})
		client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkToolCall("slow-1", "slow", `{}`)}, {runnertest.ChunkText("done")}})
		sink := newRecordingSink()
		outputs := &recordingToolOutputSink{}
		r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(sink), runner.WithToolOutputSink(outputs), runner.WithToolTimeout(time.Second))
		done := make(chan runner.TaskResult, 1)
		ctx := t.Context()
		go func() { done <- r.Run(ctx, runner.TaskSpec{Prompt: "go"}) }()
		<-expired
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("run returned before execution settled")
		default:
		}
		close(release)
		result := <-done
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		failures := sink.toolFailedEvents()
		if len(failures) != 1 || failures[0].Abandoned {
			t.Fatalf("terminal events: %+v", failures)
		}
		if failures[0].Kind != tools.Kinds.TRANSIENT || !errors.Is(failures[0].Err, context.DeadlineExceeded) || failures[0].RawOutput != "CANARY-late-result" {
			t.Fatalf("timeout classification/raw output: %+v", failures[0])
		}
		if len(outputs.records) != 1 || outputs.records[0].Output != "CANARY-late-result" {
			t.Fatalf("late history: %+v", outputs.records)
		}
		history := outputs.records[0]
		if history.Success || history.Kind != tools.Kinds.TRANSIENT || !strings.Contains(history.Error, "exceeded the per-tool time budget") {
			t.Fatalf("late history terminal classification: %+v", history)
		}
		var message string
		for _, m := range result.Messages {
			if m.Role == llm.RoleTool {
				message = m.Content
			}
		}
		if !strings.Contains(message, "exceeded the per-tool time budget") {
			t.Fatalf("model timeout: %q", message)
		}
	})
}

func (s *recordingSink) toolFailedEvents() []runner.ToolFailed {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]runner.ToolFailed(nil), s.toolFails...)
}
