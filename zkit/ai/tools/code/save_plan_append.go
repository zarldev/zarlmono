package code

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// SavePlanAppendTool appends content to a plan file under
// .zarlcode/plans/<name>.md. It is the fallback path for plans
// larger than the [SavePlanTool] one-shot cap, and mirrors the
// [WriteTool] → [WriteAppendTool] relationship — same scaffold-then-
// chunk recipe, narrowed to the plans directory so the read-only
// invariant of plan mode is preserved.
//
// # Why a dedicated append for plans (not just write_append)
//
// write_append's path is unconstrained inside the workspace, so
// plan mode can't expose it without re-opening the "models can use
// the carve-out to mutate arbitrary files" door save_plan was
// designed to close. save_plan_append's path is locked the same way
// save_plan's is: same slug regex, same join under [PlansDir], same
// rejection of slashes / dots / traversal.
//
// Usage pattern when a plan exceeds [maxWriteContentBytes]:
//
//	save_plan(name, "")                                  // empty scaffold
//	save_plan_append(name, "<up to ~256KB chunk>")       // chunk 1
//	save_plan_append(name, "<up to ~256KB chunk>")       // chunk 2
//	... etc
//
// No state is tracked between calls — the tool is stateless and
// just opens the file in append mode each time. The running file
// size is reported back in the success message so the model can
// sanity-check progress without re-reading the file.
type SavePlanAppendTool struct{ ws Workspace }

// SavePlanAppendArgs is the typed argument struct
// SavePlanAppendTool.Execute decodes into via tools.DecodeArgs.
type SavePlanAppendArgs struct {
	Name    string `json:"name" doc:"Filename slug (without extension) of the plan to append to. Same shape as save_plan's name: lowercase letters, digits, dash, underscore. Must already have been scaffolded with save_plan; this tool does not infer a default timestamp slug."`
	Content string `json:"content" doc:"Bytes to append. Keep each call under the configured cap (256KB default)."`
}

// NewSavePlanAppendTool returns the tool that appends steps to an
// existing plan artifact in ws.
func NewSavePlanAppendTool(ws Workspace) *SavePlanAppendTool { return &SavePlanAppendTool{ws: ws} }

// Definition advertises save_plan_append with name (required slug) and
// content; like save_plan it leaves Mutates unset — appends land only
// under the plans directory.
func (t *SavePlanAppendTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameSavePlanAppend,
		Description: "Append a chunk of markdown to .zarlcode/plans/<name>.md. " +
			"Use this when a plan exceeds the save_plan one-shot cap: scaffold with " +
			"save_plan(name, \"\"), then call save_plan_append repeatedly with chunks " +
			"under the cap. Path is locked to the plans directory; this tool cannot " +
			"modify any other file.",
		Parameters: tools.SchemaFor[SavePlanAppendArgs](),
	}
}

// Execute requires both name and content, caps each chunk at
// maxAppendContentBytes (256KB default), enforces the same
// safePlanName slug rules as save_plan, then appends to
// .zarlcode/plans/<name>.md under the path lock — creating the parent
// directory and file when the scaffold step was skipped. Success
// reports the running file size.
func (t *SavePlanAppendTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[SavePlanAppendArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Content == "" {
		return tools.Failure(call.ID, tools.Validation("save_plan_append", "content required")), nil
	}
	// Same cap rationale as write_append — a model that calls this
	// with one giant chunk hits the very streaming-arg failure mode
	// the chunked path was supposed to dodge.
	if maxAppendContentBytes > 0 && len(args.Content) > maxAppendContentBytes {
		return tools.Failure(call.ID, tools.Validation("save_plan_append", fmt.Sprintf(
			"chunk too large (%d bytes; cap %d bytes). "+
				"Split into multiple save_plan_append calls each up to %d bytes.",
			len(args.Content), maxAppendContentBytes, maxAppendContentBytes))), nil
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return tools.Failure(call.ID, tools.Validation("save_plan_append",
			"name required — append targets an existing plan file scaffolded by save_plan")), nil
	}
	if strings.ContainsAny(name, "/\\") {
		return tools.Failure(call.ID, tools.Validation("save_plan_append", fmt.Sprintf(
			"name %q must not contain slashes — files always land in .zarlcode/plans/",
			name))), nil
	}
	if !safePlanName.MatchString(name) {
		return tools.Failure(call.ID, tools.Validation("save_plan_append", fmt.Sprintf(
			"name %q must match [a-z0-9][a-z0-9_-]{0,63} "+
				"(lowercase letters, digits, dash, underscore; no spaces, no dots)",
			name))), nil
	}

	rel := filepath.Join(PlansDir, name+".md")
	abs, err := t.ws.Resolve(rel)
	if err != nil {
		// Parent may not exist yet if the caller skipped the
		// save_plan scaffold. Fall back to a plain join with a
		// contains-root check; MkdirParentInRoot below creates it.
		abs = filepath.Join(t.ws.Root(), rel)
		if !strings.HasPrefix(abs, t.ws.Root()) {
			return tools.Failure(
				call.ID,
				tools.Permission("save_plan_append", fmt.Sprintf("resolve %q: %v", rel, err)),
			), nil
		}
	}

	unlock := t.ws.LockPath(abs)
	defer unlock()
	if err := t.ws.MkdirParentInRoot(abs); err != nil {
		return tools.Failure(call.ID, tools.Fatal("save_plan_append", fmt.Errorf("mkdir %q: %w", rel, err))), nil
	}
	f, err := t.ws.OpenFileInRoot(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filesystem.ModePublicFile)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("save_plan_append", fmt.Errorf("open %q: %w", rel, err))), nil
	}
	defer func() { _ = f.Close() }()
	n, err := f.WriteString(args.Content)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("save_plan_append", fmt.Errorf("%q: %w", rel, err))), nil
	}
	info, statErr := os.Stat(abs)
	if statErr == nil {
		return tools.Success(call.ID, fmt.Sprintf("appended %d bytes to %s (file size now %d)", n, rel, info.Size())), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("appended %d bytes to %s", n, rel)), nil
}
