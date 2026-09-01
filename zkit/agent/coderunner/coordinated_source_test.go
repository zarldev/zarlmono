package coderunner_test

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestCoordinateWorkspaceWaitsForConflictingOwner(t *testing.T) {
	inner := &accessSource{spec: tools.ToolSpec{Name: "read_workspace", WorkspaceAccess: tools.WorkspaceAccesses.READ}}
	coordinator := tools.NewWorkspaceCoordinator()
	source := coderunner.CoordinateWorkspace(inner, coordinator)

	writer, err := coordinator.Acquire("child", tools.WorkspaceAccesses.WRITE)
	if err != nil {
		t.Fatalf("acquire child writer: %v", err)
	}

	result := make(chan *tools.ToolResult, 1)
	go func() {
		ctx := taskscope.WithID(t.Context(), "parent")
		res, _ := source.Execute(ctx, tools.ToolCall{ID: "parent-read", ToolName: "read_workspace"})
		result <- res
	}()
	select {
	case res := <-result:
		t.Fatalf("conflicting call completed before release: %#v", res)
	case <-time.After(20 * time.Millisecond):
	}
	writer.Release()
	select {
	case res := <-result:
		if res == nil || !res.Success {
			t.Fatalf("result after release = %#v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("conflicting call did not wake after release")
	}
}

func TestCoordinateWorkspaceKeepsOpaqueEffectsWorkspaceWide(t *testing.T) {
	inner := &accessSource{spec: tools.ToolSpec{
		Name:             "bash",
		WorkspaceAccess:  tools.WorkspaceAccesses.WRITE,
		AffectsWorkspace: true,
	}}
	coordinator := tools.NewWorkspaceCoordinator()
	other, err := coordinator.AcquirePaths("other", tools.WorkspaceAccesses.READ, []string{"zarlcode"})
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan *tools.ToolResult, 1)
	go func() {
		ctx := taskscope.WithID(t.Context(), "child")
		res, _ := coderunner.CoordinateWorkspace(inner, coordinator).Execute(ctx, tools.ToolCall{ID: "bash", ToolName: "bash"})
		result <- res
	}()
	select {
	case res := <-result:
		t.Fatalf("opaque call completed before release: %#v", res)
	case <-time.After(20 * time.Millisecond):
	}
	other.Release()
	select {
	case res := <-result:
		if res == nil || !res.Success {
			t.Fatalf("opaque result after release = %#v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("opaque call did not wake after release")
	}
}

func TestCoordinateWorkspaceInfersMutationPath(t *testing.T) {
	inner := &accessSource{spec: tools.ToolSpec{
		Name:            "write",
		WorkspaceAccess: tools.WorkspaceAccesses.WRITE,
		Mutates:         true,
		WorkspaceScope:  tools.WorkspaceScopeArgument("path"),
	}}
	coordinator := tools.NewWorkspaceCoordinator()
	other, err := coordinator.AcquirePaths("other", tools.WorkspaceAccesses.WRITE, []string{"zarlcode"})
	if err != nil {
		t.Fatal(err)
	}

	source := coderunner.CoordinateWorkspace(inner, coordinator)
	ctx := taskscope.WithID(t.Context(), "child")
	result, err := source.Execute(ctx, tools.ToolCall{
		ID: "write", ToolName: "write", Arguments: tools.ToolParameters{"path": "zkit/file.go"},
	})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("disjoint inferred mutation = (%#v, %v), want success", result, err)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	result, err = source.Execute(cancelCtx, tools.ToolCall{
		ID: "write-overlap", ToolName: "write", Arguments: tools.ToolParameters{"path": "zarlcode/file.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.TRANSIENT || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancelled inferred mutation = %#v, want transient cancellation", result)
	}
	other.Release()
}

func TestCoordinateWorkspaceInfersAllApplyPatchPaths(t *testing.T) {
	inner := &accessSource{spec: tools.ToolSpec{
		Name:            code.ToolNameApplyPatch,
		WorkspaceAccess: tools.WorkspaceAccesses.WRITE,
		Mutates:         true,
		WorkspaceScope:  tools.WorkspaceScopePatch("patch"),
	}}
	coordinator := tools.NewWorkspaceCoordinator()
	other, err := coordinator.AcquirePaths("other", tools.WorkspaceAccesses.WRITE, []string{"zarlcode"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()

	patch := "*** Begin Patch\n*** Update File: zkit/file.go\n@@\n-old\n+new\n*** Update File: zarlcode/file.go\n@@\n-old\n+new\n*** End Patch"
	ctx, cancel := context.WithTimeout(taskscope.WithID(t.Context(), "child"), 20*time.Millisecond)
	defer cancel()
	result, err := coderunner.CoordinateWorkspace(inner, coordinator).Execute(ctx, tools.ToolCall{
		ID: "patch", ToolName: code.ToolNameApplyPatch, Arguments: tools.ToolParameters{"patch": patch},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.TRANSIENT || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("timed-out multi-path patch = %#v, want transient deadline", result)
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
