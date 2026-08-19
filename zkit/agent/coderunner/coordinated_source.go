package coderunner

import (
	"context"
	"fmt"
	"iter"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// coordinatedSource acquires short-lived workspace access for each tool call.
// Long-lived agent leases use the same task ID, so child calls reenter their
// owner's lease while conflicting parent/child calls fail without blocking.
type coordinatedSource struct {
	inner       tools.Source
	coordinator *tools.WorkspaceCoordinator
}

// CoordinateWorkspace returns a source governed by coordinator. A nil
// coordinator preserves the original source.
func CoordinateWorkspace(inner tools.Source, coordinator *tools.WorkspaceCoordinator) tools.Source {
	if inner == nil || coordinator == nil {
		return inner
	}
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
	lease, err := s.coordinator.Acquire(owner, spec.Access())
	if err != nil {
		return tools.Failure(call.ID, tools.Budget(call.ToolName.String(), fmt.Sprintf("workspace access: %v", err))), nil
	}
	defer lease.Release()
	return s.inner.Execute(ctx, call)
}

var _ tools.Source = coordinatedSource{}
