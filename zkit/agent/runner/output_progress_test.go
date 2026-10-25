package runner_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type outputReportingTool struct {
	updates []string
	report  func(context.Context, tools.ToolOutputUpdate)
}

func (t *outputReportingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "report", Description: "reports invocation output"}
}

func (t *outputReportingTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	t.report = call.ReportOutput
	for _, text := range t.updates {
		call.ReportOutput(ctx, tools.ToolOutputUpdate{Text: text})
	}
	return tools.Success(call.ID, "final result"), nil
}

type nestedOutputReportingTool struct{}

func (nestedOutputReportingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "nested_report", Description: "reports child invocation output"}
}

func (nestedOutputReportingTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	observer := tools.NestedToolObserverFromContext(ctx)
	event := tools.NestedToolCall{
		ParentID: call.ID, ChildID: "child", Sequence: 3,
		Call: tools.ToolCall{ID: "child", ToolName: "child_report"},
	}
	observer.OnNestedToolStarted(ctx, event)
	bound := observer.(tools.NestedToolCallBinder).BindNestedToolCall(ctx, event)
	event.Call = bound
	bound.ReportOutput(ctx, tools.ToolOutputUpdate{Text: "child preview"})
	observer.OnNestedToolFinished(ctx, tools.NestedToolResult{NestedToolCall: event, Result: tools.Success(bound.ID, "child final")})
	bound.ReportOutput(ctx, tools.ToolOutputUpdate{Text: "late child preview"})
	return tools.Success(call.ID, "parent final"), nil
}

func TestRunnerPublishesBoundedInvocationOutputBeforeTerminal(t *testing.T) {
	tool := &outputReportingTool{updates: []string{"one", "one\ntwo"}}
	history := &recordingToolOutputSink{}
	sink := runnertest.NewSink()
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("call", "report", `{}`)},
		{runnertest.ChunkText("done")},
	})
	r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(sink), runner.WithToolOutputSink(history))
	if result := r.Run(t.Context(), runner.TaskSpec{ID: "task", Depth: 2, Prompt: "go"}); result.Err != nil {
		t.Fatal(result.Err)
	}

	updates := sink.ToolOutputs()
	if len(updates) != 2 {
		t.Fatalf("output updates = %d, want 2", len(updates))
	}
	completed, ok := sink.FirstToolCompleted()
	if !ok {
		t.Fatal("missing completion")
	}
	for i, update := range updates {
		if update.TaskID != "task" || update.Depth != 2 || update.ExecutionID == "" ||
			update.ExecutionID != completed.ExecutionID || update.ToolID != "call" || update.ToolName != "report" ||
			update.ParentExecutionID != "" || update.ParentToolID != "" || update.Sequence != 0 || update.Text != tool.updates[i] {
			t.Fatalf("update %d = %#v", i, update)
		}
	}

	tool.report(t.Context(), tools.ToolOutputUpdate{Text: "too late"})
	if got := len(sink.ToolOutputs()); got != 2 {
		t.Fatalf("post-terminal update published: %d events", got)
	}
	if len(history.records) != 1 || history.records[0].Output != "final result" {
		t.Fatalf("progress entered raw tool history: %#v", history.records)
	}
}

func TestRunnerBindsNestedOutputToExactChildExecution(t *testing.T) {
	sink := runnertest.NewSink()
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("outer", "nested_report", `{}`)},
		{runnertest.ChunkText("done")},
	})
	result := runner.New(client, runner.WithTools(tools.NewRegistry(nestedOutputReportingTool{})), runner.WithSink(sink)).Run(
		t.Context(), runner.TaskSpec{ID: "task", Prompt: "go"},
	)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	updates := sink.ToolOutputs()
	if len(updates) != 1 {
		t.Fatalf("nested updates = %#v", updates)
	}
	update := updates[0]
	if update.ToolID != "child" || update.ToolName != "child_report" || update.ParentToolID != "outer" ||
		update.ExecutionID == "" || update.ParentExecutionID == "" || update.ExecutionID == update.ParentExecutionID ||
		update.Sequence != 3 || update.Text != "child preview" {
		t.Fatalf("nested update = %#v", update)
	}
}

func TestRunnerBoundsAndRepairsInvocationOutput(t *testing.T) {
	unsafe := strings.Repeat("a", tools.MaxToolOutputPreviewBytes-1) + "é" + string([]byte{0xff})
	tool := &outputReportingTool{updates: []string{unsafe}}
	sink := runnertest.NewSink()
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("call", "report", `{}`)},
		{runnertest.ChunkText("done")},
	})
	result := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	updates := sink.ToolOutputs()
	if len(updates) != 1 {
		t.Fatalf("bounded update count = %d", len(updates))
	}
	if len(updates[0].Text) > tools.MaxToolOutputPreviewBytes || !utf8.ValidString(updates[0].Text) {
		t.Fatalf("bounded update = bytes %d, valid %v", len(updates[0].Text), utf8.ValidString(updates[0].Text))
	}
}
