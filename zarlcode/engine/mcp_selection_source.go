package engine

import (
	"context"
	"iter"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

// Borrowed fallback children may invoke already advertised remotes, but cannot
// change the owning session's selection. Named children receive separate copies.
type mcpSelectionScope struct{ tools.Source }

func isMCPSelection(name tools.ToolName) bool {
	return name == dynamic.ToolNameMCPSearch || name == dynamic.ToolNameMCPActivate || name == dynamic.ToolNameMCPDeactivate
}

func (s *mcpSelectionScope) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) {
		for tool := range s.Source.Tools(ctx) {
			if taskscope.DepthFrom(ctx) != 0 && isMCPSelection(tool.Definition().Name) {
				continue
			}
			if !yield(tool) {
				return
			}
		}
	}
}

func (s *mcpSelectionScope) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if taskscope.DepthFrom(ctx) != 0 && isMCPSelection(call.ToolName) {
		return nil, tools.Permission(string(call.ToolName), "only the root task may change session MCP selection")
	}
	return s.Source.Execute(ctx, call)
}

func (s *mcpSelectionScope) ForgetTask(id taskscope.ID) {
	if owner, ok := s.Source.(interface{ ForgetTask(taskscope.ID) }); ok {
		owner.ForgetTask(id)
	}
}

func (l *LiveRunner) childMCPExposure() *dynamic.MCPExposure {
	l.mu.Lock()
	parent, registry := l.mcpExposure, l.mcp
	l.mu.Unlock()
	if parent == nil { // MCP may be omitted by the composition root.
		return nil
	}
	child := dynamic.NewMCPExposure(registry)
	// Snapshot is an already validated, owned value from this same implementation.
	// Replace cannot reject it; no untrusted input crosses this construction path.
	_ = child.Replace(parent.Snapshot())
	return child
}
