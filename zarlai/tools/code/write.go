package code

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// WriteTool overwrites (or creates) a file inside the workspace.
type WriteTool struct{ ws Workspace }

func NewWriteTool(ws Workspace) *WriteTool { return &WriteTool{ws: ws} }

func (t *WriteTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "write",
		Description: "Write or overwrite a file in the workspace. Creates parent directories as needed.",
		Parameters: service.Parameters{
			{Name: "path", Type: service.ParamString, Description: "Path relative to workspace root.", Required: true},
			{Name: "content", Type: service.ParamString, Description: "Full file contents to write.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *WriteTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	path := call.Arguments.String("path", "")
	content := call.Arguments.String("content", "")

	abs, err := t.ws.Resolve(path)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("write", err.Error())), nil
	}
	if err := t.ws.WriteFileInRoot(abs, []byte(content), 0o644); err != nil {
		return tools.Failure(call.ID, tools.Transient("write", fmt.Errorf("write %q: %w", path, err))), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("wrote %d bytes to %s", len(content), path)), nil
}
