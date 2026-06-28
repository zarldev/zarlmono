package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/instructions"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool names for the lazy instruction-loading surface.
const (
	ToolNameListInstructions tools.ToolName = "list_instructions"
	ToolNameLoadInstruction  tools.ToolName = "load_instruction"
)

// listInstructionsTool enumerates available nested instruction docs (the lazy
// index), mirroring list_skills/list_agents for instructions.
type listInstructionsTool struct {
	nested func() []instructions.NestedDoc
}

func NewListInstructionsTool(nested func() []instructions.NestedDoc) *listInstructionsTool {
	return &listInstructionsTool{nested: nested}
}

func (t *listInstructionsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameListInstructions,
		Description: "Return the nested workspace instruction index as labelled plaintext — one entry " +
			"per instruction file below the workspace root with its relative path. Call only when " +
			"the user asks about workspace guidance or when a task clearly depends on a specific " +
			"module's conventions.",
		Parameters: llm.SchemaFromMap(map[string]any{
			schemaType:       schemaTypeObject,
			schemaProperties: map[string]any{},
			schemaAdditional: false,
		})}
}

func (t *listInstructionsTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	nested := t.nested()
	return &tools.ToolResult{
		ToolCallID: call.ID,
		Success:    true,
		Data:       renderNestedInstructions(nested),
		ExecutedAt: time.Now(),
	}, nil
}

func renderNestedInstructions(nested []instructions.NestedDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nested instruction files: %d\n", len(nested))
	if len(nested) == 0 {
		b.WriteString("(none discovered)")
		return b.String()
	}
	nameWidth := 0
	for _, n := range nested {
		if w := ansi.StringWidth(n.RelPath); w > nameWidth {
			nameWidth = w
		}
	}
	for _, n := range nested {
		pad := strings.Repeat(" ", nameWidth-ansi.StringWidth(n.RelPath))
		fmt.Fprintf(&b, "  %s%s\n", n.RelPath, pad)
	}
	return strings.TrimRight(b.String(), "\n")
}

// loadInstructionTool loads a nested instruction doc's full body into context,
// mirroring load_skill for instruction files.
type loadInstructionTool struct {
	wsRoot string
	nested func() []instructions.NestedDoc
}

func NewLoadInstructionTool(wsRoot string, nested func() []instructions.NestedDoc) *loadInstructionTool {
	return &loadInstructionTool{wsRoot: wsRoot, nested: nested}
}

type loadInstructionArgs struct {
	Path string `json:"path"`
}

func (t *loadInstructionTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameLoadInstruction,
		Description: "Load a nested AGENTS.md / CLAUDE.md instruction file's full body by its " +
			"workspace-relative path. Use this only when the user asks about a specific module's " +
			"guidance or after listing instructions to choose one; do not guess paths and do not " +
			"use read(<path>) for instruction bodies.",
		Parameters: llm.SchemaFromMap(map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"path": map[string]any{
					schemaType:    "string",
					"description": "Workspace-relative path from list_instructions.",
				},
			},
			"required":       []string{"path"},
			schemaAdditional: false,
		})}
}

func (t *loadInstructionTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args loadInstructionArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return tools.Failure(call.ID, tools.Validation("load_instruction", "path is required")), nil
	}

	doc, err := instructions.LoadOne(t.wsRoot, path)
	if err != nil {
		// List the known paths so the model can recover.
		known := []string{}
		for _, n := range t.nested() {
			known = append(known, n.RelPath)
		}
		return tools.Failure(call.ID, tools.NotFound("load_instruction", fmt.Sprintf(
			"no instruction doc at %q: %v. Available: %s", path, err, strings.Join(known, ", ")))), nil
	}

	return &tools.ToolResult{
		ToolCallID: call.ID,
		Success:    true,
		Data:       doc.Content,
		ExecutedAt: time.Now(),
	}, nil
}
