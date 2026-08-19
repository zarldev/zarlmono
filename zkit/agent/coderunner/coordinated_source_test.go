package coderunner_test

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestCoordinateWorkspaceSharesOwnerAndRejectsConflicts(t *testing.T) {
	inner := &accessSource{spec: tools.ToolSpec{Name: "read_workspace", WorkspaceAccess: tools.WorkspaceAccesses.READ}}
	coordinator := tools.NewWorkspaceCoordinator()
	source := coderunner.CoordinateWorkspace(inner, coordinator)

	writer, err := coordinator.Acquire("child", tools.WorkspaceAccesses.WRITE)
	if err != nil {
		t.Fatalf("acquire child writer: %v", err)
	}
	defer writer.Release()

	parentCtx := taskscope.WithID(t.Context(), "parent")
	res, err := source.Execute(parentCtx, tools.ToolCall{ID: "parent-read", ToolName: "read_workspace"})
	if err != nil {
		t.Fatalf("parent Execute Go error: %v", err)
	}
	if res == nil || res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), tools.ErrWorkspaceConflict.Error()) {
		t.Fatalf("parent conflict result = %#v", res)
	}

	childCtx := taskscope.WithID(t.Context(), "child")
	res, err = source.Execute(childCtx, tools.ToolCall{ID: "child-read", ToolName: "read_workspace"})
	if err != nil || res == nil || !res.Success {
		t.Fatalf("owner reentry = (%#v, %v)", res, err)
	}
}

type accessSource struct{ spec tools.ToolSpec }

func (s *accessSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) { yield(accessTool{source: s}) }
}

func (s *accessSource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(call.ID, "ok"), nil
}

type accessTool struct{ source *accessSource }

func (t accessTool) Definition() tools.ToolSpec { return t.source.spec }
func (t accessTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return t.source.Execute(ctx, call)
}
