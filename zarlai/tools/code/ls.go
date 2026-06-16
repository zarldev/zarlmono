package code

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// LsTool lists workspace directory entries with structured output.
// Hidden entries (starting with ".") are excluded by default.
//
// Note: .gitignore awareness in v1 is delegated to grep (which uses rg
// natively). Adding gitignore filtering here is tracked as a follow-up.
type LsTool struct{ ws Workspace }

func NewLsTool(ws Workspace) *LsTool { return &LsTool{ws: ws} }

func (t *LsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "ls",
		Description: "List a workspace directory. Returns JSON [{name, type, size}]. Hidden entries excluded unless show_hidden is true.",
		Parameters: service.Parameters{
			{Name: "path", Type: service.ParamString, Description: "Directory inside workspace (default = root).", Required: false},
			{Name: "show_hidden", Type: service.ParamBool, Description: "Include dotfiles.", Required: false},
		}.ToJSONSchema(),
	}
}

type lsEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

func (t *LsTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	target := t.ws.Root()
	if p := call.Arguments.String("path", ""); p != "" && p != "." {
		abs, err := t.ws.Resolve(p)
		if err != nil {
			return tools.Failure(call.ID, tools.Validation("ls", err.Error())), nil
		}
		target = abs
	}
	showHidden := call.Arguments.Bool("show_hidden", false)

	entries, err := t.ws.ReadDirInRoot(target)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("ls", fmt.Errorf("ls %q: %w", target, err))), nil
	}

	out := make([]lsEntry, 0, len(entries))
	for _, e := range entries {
		if !showHidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		typ := "file"
		switch {
		case e.IsDir():
			typ = "dir"
		case info.Mode()&os.ModeSymlink != 0:
			typ = "symlink"
		}
		out = append(out, lsEntry{Name: e.Name(), Type: typ, Size: info.Size()})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("ls", fmt.Errorf("ls marshal: %w", err))), nil
	}
	return tools.Success(call.ID, string(body)), nil
}
