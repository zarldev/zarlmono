package program_test

import (
	"context"
	"testing"

	program "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type bindingNestedObserver struct {
	started  bool
	bound    tools.NestedToolCall
	finished tools.NestedToolResult
	updates  []string
}

func (o *bindingNestedObserver) OnNestedToolStarted(_ context.Context, _ tools.NestedToolCall) {
	o.started = true
}

func (o *bindingNestedObserver) BindNestedToolCall(_ context.Context, event tools.NestedToolCall) tools.ToolCall {
	o.bound = event
	call := event.Call
	call.ExecutionID = "child-execution"
	call.ReportOutput = func(_ context.Context, update tools.ToolOutputUpdate) {
		o.updates = append(o.updates, update.Text)
	}
	return call
}

func (o *bindingNestedObserver) OnNestedToolFinished(_ context.Context, event tools.NestedToolResult) {
	o.finished = event
}

func TestProgramBindsNestedInvocationAfterStartBeforeExecute(t *testing.T) {
	observer := &bindingNestedObserver{}
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"report": {spec: tools.ToolSpec{Name: "report"}, fn: func(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			if !observer.started {
				t.Error("nested call executed before start observation")
			}
			if call.ExecutionID != "child-execution" || call.ReportOutput == nil {
				t.Fatalf("nested call was not bound: %#v", call)
			}
			call.ReportOutput(ctx, tools.ToolOutputUpdate{Text: "preview"})
			return tools.Success(call.ID, call.Arguments["value"]), nil
		}},
	}}
	source, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	ctx := tools.ContextWithNestedToolObserver(t.Context(), observer)
	result, err := source.Execute(ctx, tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{
		"script": `emit(call("report", {"value":"kept"}))`,
	}})
	if err != nil || !result.Success {
		t.Fatalf("program result = %#v, %v", result, err)
	}
	if observer.bound.ChildID != "outer/0" || observer.bound.Call.Arguments.String("value", "") != "kept" {
		t.Fatalf("bound event = %#v", observer.bound)
	}
	if len(observer.updates) != 1 || observer.updates[0] != "preview" {
		t.Fatalf("updates = %#v", observer.updates)
	}
	if observer.finished.Call.ExecutionID != "child-execution" || observer.finished.Call.Arguments.String("value", "") != "kept" {
		t.Fatalf("finished event = %#v", observer.finished)
	}
}
