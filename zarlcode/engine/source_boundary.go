package engine

import (
	"context"
	"iter"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// toolBoundarySource routes selected synthetic tool names through boundary and
// every other name through direct. It lets a composite tool added after the
// ordinary guardrail chain receive outer-call policy without double-guarding
// direct or nested calls.
type toolBoundarySource struct {
	direct   tools.Source
	boundary tools.Source
	names    map[tools.ToolName]struct{}
}

// RouteToolBoundary routes selected tool names through boundary and all other
// calls through direct. Tools are listed from direct, and task cleanup is owned
// by boundary to avoid forwarding cleanup through direct twice.
func RouteToolBoundary(direct, boundary tools.Source, names ...tools.ToolName) tools.Source {
	selected := make(map[tools.ToolName]struct{}, len(names))
	for _, name := range names {
		selected[name] = struct{}{}
	}
	return &toolBoundarySource{direct: direct, boundary: boundary, names: selected}
}

func (s *toolBoundarySource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return s.direct.Tools(ctx)
}

func (s *toolBoundarySource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if _, ok := s.names[call.ToolName]; ok {
		return s.boundary.Execute(ctx, call)
	}
	return s.direct.Execute(ctx, call)
}

func (s *toolBoundarySource) ForgetTask(id taskscope.ID) {
	// boundary wraps direct and owns cleanup forwarding through the entire
	// chain. Calling both branches would clear direct state twice.
	if forgetter, ok := s.boundary.(interface{ ForgetTask(taskscope.ID) }); ok {
		forgetter.ForgetTask(id)
	}
}
