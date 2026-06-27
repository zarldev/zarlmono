package trace_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/trace"
	"github.com/zarldev/zarlmono/zkit/agent/workflow"
)

func TestSinkExportsRunnerEvents(t *testing.T) {
	var events []trace.Event
	sink := &trace.Sink{Exporter: trace.ExporterFunc(func(_ context.Context, e trace.Event) error { events = append(events, e); return nil })}
	sink.OnToolStarted(runner.ToolStarted{ToolName: "search", ToolID: "1", Depth: 2})
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	if events[0].Kind != trace.KindToolStarted || events[0].Name != "search" || events[0].Depth != 2 {
		t.Fatalf("event = %#v", events[0])
	}
}

func TestJSONLExporter(t *testing.T) {
	var b strings.Builder
	exporter := trace.NewJSONLExporter(&b)
	if err := exporter.Export(t.Context(), trace.Event{Kind: trace.KindContent, Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"kind":"content"`) {
		t.Fatalf("jsonl = %q", b.String())
	}
}

func TestWorkflowSinkExportsEvents(t *testing.T) {
	var events []trace.Event
	sink := &trace.WorkflowSink{Exporter: trace.ExporterFunc(func(_ context.Context, e trace.Event) error { events = append(events, e); return nil })}
	sink.OnWorkflowNodeCompleted(workflow.NodeCompleted{Node: "n", Output: 1})
	if len(events) != 1 || events[0].Kind != trace.KindWorkflowNodeDone || events[0].Name != "n" {
		t.Fatalf("events = %#v", events)
	}
}

func TestSinkCapturesExporterError(t *testing.T) {
	want := errors.New("boom")
	sink := &trace.Sink{Exporter: trace.ExporterFunc(func(context.Context, trace.Event) error { return want })}
	sink.OnContent(runner.Content{Delta: "x"})
	if !errors.Is(sink.Err(), want) {
		t.Fatalf("Err() = %v, want %v", sink.Err(), want)
	}
}
