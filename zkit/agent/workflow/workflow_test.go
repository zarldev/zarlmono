package workflow_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/workflow"
)

func TestWorkflowInvoke(t *testing.T) {
	g := workflow.NewGraph()
	if err := workflow.AddNode(g, "double", workflow.NodeFunc[int, int](func(_ context.Context, n int) (int, error) { return n * 2, nil })); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddNode(g, "label", workflow.NodeFunc[int, string](func(_ context.Context, n int) (string, error) { return "value", nil })); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge(workflow.Start, "double"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("double", "label"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("label", workflow.End); err != nil {
		t.Fatal(err)
	}
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	out, state, err := r.InvokeState(t.Context(), 21)
	if err != nil {
		t.Fatal(err)
	}
	if out != "value" {
		t.Fatalf("out = %#v", out)
	}
	if len(state.Path) != 2 || state.Path[0] != "double" || state.Values["double"] != 42 {
		t.Fatalf("state = %#v", state)
	}
}

func TestWorkflowConditionalEdge(t *testing.T) {
	g := workflow.NewGraph()
	_ = workflow.AddNode(g, "inc", workflow.NodeFunc[int, int](func(_ context.Context, n int) (int, error) { return n + 1, nil }))
	_ = g.AddEdge(workflow.Start, "inc")
	_ = g.AddConditionalEdge("inc", func(_ context.Context, state workflow.State) (string, error) {
		if state.Values["inc"].(int) >= 2 {
			return workflow.End, nil
		}
		return "inc", nil
	})
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Invoke(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if out != 2 {
		t.Fatalf("out = %#v", out)
	}
}

type eventSink struct{ events []string }

func (s *eventSink) OnWorkflowStarted(workflow.Started) { s.events = append(s.events, "start") }
func (s *eventSink) OnWorkflowNodeStarted(e workflow.NodeStarted) {
	s.events = append(s.events, "node-start:"+e.Node)
}
func (s *eventSink) OnWorkflowNodeCompleted(e workflow.NodeCompleted) {
	s.events = append(s.events, "node-done:"+e.Node)
}
func (s *eventSink) OnWorkflowNodeFailed(e workflow.NodeFailed) {
	s.events = append(s.events, "node-fail:"+e.Node)
}
func (s *eventSink) OnWorkflowCompleted(workflow.Completed) { s.events = append(s.events, "done") }
func (s *eventSink) OnWorkflowFailed(workflow.Failed)       { s.events = append(s.events, "fail") }

func TestWorkflowSinkEvents(t *testing.T) {
	g := workflow.NewGraph()
	_ = workflow.AddNode(g, "id", workflow.NodeFunc[int, int](func(_ context.Context, n int) (int, error) { return n, nil }))
	_ = g.AddEdge(workflow.Start, "id")
	_ = g.AddEdge("id", workflow.End)
	r, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	sink := &eventSink{}
	r.Sink = sink
	if _, err := r.Invoke(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	want := []string{"start", "node-start:id", "node-done:id", "done"}
	if !reflect.DeepEqual(sink.events, want) {
		t.Fatalf("events = %#v", sink.events)
	}
}
