package engine_test

import (
	"context"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type boundaryTestSource struct {
	name       tools.ToolName
	executions int
	forgotten  int
}

func (s *boundaryTestSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) { _ = yield(sourceFakeTool{name: s.name}) }
}

func (s *boundaryTestSource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	s.executions++
	return tools.Success(call.ID, s.name), nil
}

func (s *boundaryTestSource) ForgetTask(taskscope.ID) { s.forgotten++ }

func TestToolBoundarySourceRoutesOnlySelectedCallsThroughBoundary(t *testing.T) {
	direct := &boundaryTestSource{name: "direct"}
	boundary := &boundaryTestSource{name: "program"}
	source := engine.RouteToolBoundary(direct, boundary, "program")

	if _, err := source.Execute(t.Context(), tools.ToolCall{ID: "p", ToolName: "program"}); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Execute(t.Context(), tools.ToolCall{ID: "d", ToolName: "read"}); err != nil {
		t.Fatal(err)
	}
	if direct.executions != 1 || boundary.executions != 1 {
		t.Fatalf("executions = direct %d, boundary %d; want 1 each", direct.executions, boundary.executions)
	}
}

func TestToolBoundarySourceCleanupUsesOwningBoundary(t *testing.T) {
	direct := &boundaryTestSource{name: "direct"}
	boundary := &boundaryTestSource{name: "program"}
	source := engine.RouteToolBoundary(direct, boundary, "program")

	source.(interface{ ForgetTask(taskscope.ID) }).ForgetTask("task")
	if direct.forgotten != 0 || boundary.forgotten != 1 {
		t.Fatalf("forgotten = direct %d, boundary %d; want 0, 1", direct.forgotten, boundary.forgotten)
	}
}
