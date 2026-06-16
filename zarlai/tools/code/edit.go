package code

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// EditTool performs a string replacement in a workspace file. Without
// replace_all, old_string must appear exactly once — otherwise the edit is
// rejected to prevent accidental whole-file rewrites.
type EditTool struct{ ws Workspace }

func NewEditTool(ws Workspace) *EditTool { return &EditTool{ws: ws} }

func (t *EditTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "edit",
		Description: "Replace exact text in a workspace file. By default old_string must occur exactly once. Set replace_all to substitute every occurrence.",
		Parameters: service.Parameters{
			{Name: "path", Type: service.ParamString, Description: "Path inside the workspace.", Required: true},
			{Name: "old_string", Type: service.ParamString, Description: "Exact text to replace.", Required: true},
			{Name: "new_string", Type: service.ParamString, Description: "Replacement text.", Required: true},
			{Name: "replace_all", Type: service.ParamBool, Description: "Replace every occurrence (default false).", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *EditTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	path := call.Arguments.String("path", "")
	old := call.Arguments.String("old_string", "")
	new_ := call.Arguments.String("new_string", "")
	replaceAll := call.Arguments.Bool("replace_all", false)

	abs, err := t.ws.Resolve(path)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("edit", err.Error())), nil
	}
	if old == "" {
		return tools.Failure(call.ID, tools.Validation("edit", fmt.Sprintf("edit %q: old_string must not be empty", path))), nil
	}

	data, err := t.ws.ReadFileInRoot(abs)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("edit", fmt.Errorf("edit read %q: %w", path, err))), nil
	}
	body := string(data)

	count := strings.Count(body, old)
	if count == 0 {
		return tools.Failure(call.ID, tools.Validation("edit", fmt.Sprintf("edit %q: old_string not found", path))), nil
	}
	if count > 1 && !replaceAll {
		return tools.Failure(call.ID, tools.Validation("edit", fmt.Sprintf("edit %q: old_string matches %d times — provide more context or set replace_all", path, count))), nil
	}

	updated := strings.Replace(body, old, new_, 1)
	if replaceAll {
		updated = strings.ReplaceAll(body, old, new_)
	}

	if err := t.ws.WriteFileInRoot(abs, []byte(updated), 0o644); err != nil {
		return tools.Failure(call.ID, tools.Transient("edit", fmt.Errorf("edit write %q: %w", path, err))), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("edited %s (%d replacement(s))", path, count)), nil
}
