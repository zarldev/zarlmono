package dynamic

import (
	"context"
	"fmt"
	"regexp"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ToolNameUnregisterTool names the agent-facing rollback tool. The
// historical RegisterTool ("register a pre-built binary") was dropped
// — `new_tool` is the canonical (and only) authoring path now, so
// the parallel "register a binary you already have" path was just
// extra surface area for the model to confuse itself with.
const ToolNameUnregisterTool tools.ToolName = "unregister_tool"

// validToolName is the shared identifier check used by every dynamic
// tool entry point (new_tool, mcp_connect, …) before mutating the
// registry. Strict on purpose: snake_case lowercase only, 2-64
// chars, must start with a letter. Anything else risks a name
// collision or an unsafe filename downstream.
var validToolName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// UnregisterTool removes a previously-registered dynamic tool. It
// only touches the catalog + registry; the binary on disk is left
// for forensics.
type UnregisterTool struct {
	registrar *Registrar
}

// UnregisterToolArgs is the typed argument struct UnregisterTool.Execute
// decodes into via tools.DecodeArgs.
type UnregisterToolArgs struct {
	Name string `json:"name" doc:"Tool name to unregister."`
}

// NewUnregisterTool returns the tool that removes a dynamic tool from r
// by name; the built binary stays on disk.
func NewUnregisterTool(r *Registrar) *UnregisterTool { return &UnregisterTool{registrar: r} }

// Definition advertises unregister_tool with a single required name
// parameter. Declares Mutates:true — removing a registration mutates
// the tool registry, so it's gated out of read-only spawn modes.
func (t *UnregisterTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameUnregisterTool,
		Description: "Remove a dynamic tool by name. The binary on disk is left in place; only the registration is removed.",
		Parameters:  tools.SchemaFor[UnregisterToolArgs](),
		// Mutates the tool registry — gated out of read-only spawn modes.
		Mutates: true,
	}
}

// Execute removes the named tool from the registrar's catalog and
// registry. Only the registration goes away — the built binary stays
// on disk for forensics.
func (t *UnregisterTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[UnregisterToolArgs](call.Arguments)
	if derr != nil {
		return failureResult(call.ID, derr.Error()), nil
	}
	if args.Name == "" {
		return failureResult(call.ID, "unregister_tool: name required"), nil
	}
	if err := t.registrar.UnregisterContext(ctx, tools.ToolName(args.Name)); err != nil {
		return failureResult(call.ID, fmt.Sprintf("unregister_tool: %v", err)), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("unregistered %s", args.Name)), nil
}
