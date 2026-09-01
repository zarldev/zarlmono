package coderunner

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// coordinatedSource acquires short-lived, automatically inferred workspace
// access for each tool call. Unknown or opaque effects fall back to the root.
type coordinatedSource struct {
	inner       tools.Source
	coordinator *tools.WorkspaceCoordinator
}

// CoordinateWorkspace returns a source governed by coordinator.
func CoordinateWorkspace(inner tools.Source, coordinator *tools.WorkspaceCoordinator) tools.Source {
	return coordinatedSource{inner: inner, coordinator: coordinator}
}
func (s coordinatedSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return s.inner.Tools(ctx)
}

func (s coordinatedSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var spec tools.ToolSpec
	found := false
	for tool := range s.inner.Tools(ctx) {
		candidate := tool.Definition()
		if candidate.Name == call.ToolName {
			spec, found = candidate, true
			break
		}
	}
	if !found {
		return s.inner.Execute(ctx, call)
	}
	owner := tools.WorkspaceOwner(taskscope.IDFrom(ctx))
	lease, err := s.coordinator.AcquirePathsWait(ctx, owner, spec.Access(), spec.WorkspaceScope.Paths(call.Arguments))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return tools.Failure(call.ID, tools.Transient(call.ToolName.String(), err)), nil
		}
		return tools.Failure(call.ID, tools.Budget(call.ToolName.String(), fmt.Sprintf("workspace access: %v", err))), nil
	}
	defer lease.Release()
	return s.inner.Execute(ctx, call)
}

var _ tools.Source = coordinatedSource{}
