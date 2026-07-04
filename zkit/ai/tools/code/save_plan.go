package code

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// PlansDir is the workspace-relative directory where save_plan writes
// markdown plan documents. Hidden under .zarlcode/ alongside the
// other zarlcode config (skills, agents, prompts) so the workspace
// root stays uncluttered. Mkdirs lazily on first save.
const PlansDir = ".zarlcode/plans"

// SavePlanTool persists a plan-mode markdown artifact to
// <workspace>/.zarlcode/plans/<name>.md.
//
// # Why a dedicated tool, not the generic write
//
// Plan mode strips write/edit/bash from the model's tool surface so
// the read-only invariant is unbreakable. But "produce a plan" is
// pointless if the plan can only live in the transcript — once the
// session compacts or the user closes the shell, it's gone.
//
// save_plan is the carve-out: a single, narrow, path-locked write
// the model can call from plan mode to drop the plan as a real
// markdown file. Writes outside [PlansDir] are rejected at the
// tool layer; the model cannot use save_plan as a back-door to
// modify arbitrary files.
//
// The tool is also available in build mode (registered
// unconditionally at startup) so a build-mode agent can save a
// retrospective or post-mortem in the same convention.
type SavePlanTool struct{ ws Workspace }

// SavePlanArgs is the typed argument struct SavePlanTool.Execute
// decodes into via tools.DecodeArgs. Field tags drive both JSON decoding
// and SchemaFor schema generation.
type SavePlanArgs struct {
	Name    string `json:"name,omitempty" doc:"Filename slug (without extension). Lowercase letters, digits, dash, underscore. Empty = a timestamp slug like \"plan-20260515-1042\". Must not contain slashes — paths are locked to .zarlcode/plans/."`
	Content string `json:"content" doc:"Full markdown body of the plan."`
}

// NewSavePlanTool returns the tool that writes a structured plan
// artifact into ws.
func NewSavePlanTool(ws Workspace) *SavePlanTool { return &SavePlanTool{ws: ws} }

// safePlanName matches lowercase / dash / underscore / digit names —
// the slug the saved file will be named with. Strict on purpose:
// rejecting anything else keeps the plans dir browsable and
// path-traversal impossible at the slug level (Resolve catches the
// rest, but defence in depth is cheap).
var safePlanName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Definition advertises save_plan with an optional name slug and
// required content; Mutates is left unset even though it writes a
// file — the write is confined to the plans directory.
func (t *SavePlanTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameSavePlan,
		Description: "Save a plan-mode artifact to .zarlcode/plans/<name>.md. " +
			"Use at the end of plan-mode work to persist the proposal as a " +
			"markdown document the user can revisit, edit, or share. Writes " +
			"are restricted to the plans directory; this tool cannot modify " +
			"any other file.",
		Parameters: tools.SchemaFor[SavePlanArgs](),
	}
}

// Execute requires content and caps it at maxWriteContentBytes (shared
// with write, 256KB default — oversized content gets a
// scaffold-plus-save_plan_append recipe), defaults an empty name to a
// plan-YYYYMMDD-HHMM timestamp slug, enforces the safePlanName regex,
// and writes <name>.md under PlansDir while holding the path lock.
func (t *SavePlanTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[SavePlanArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Content == "" {
		return tools.Failure(call.ID, tools.Validation("save_plan", "content required")), nil
	}
	// Same generous cap as WriteTool — plans rarely exceed a few KB,
	// but oversized streaming-arg failures should produce the same
	// clear error rather than a silent truncation. Mirror write's
	// scaffold-then-append recovery recipe so an oversized plan
	// recovers on the next turn instead of looping on this error.
	if maxWriteContentBytes > 0 && len(args.Content) > maxWriteContentBytes {
		// Resolve the slug the caller would have used so the recipe
		// is copy-pasteable rather than a generic placeholder.
		hint := strings.TrimSpace(args.Name)
		if hint == "" {
			hint = "<name>"
		}
		return tools.Failure(call.ID, tools.Validation("save_plan", fmt.Sprintf(
			"content too large (%d bytes; cap %d bytes). "+
				"Scaffold first with save_plan(name=%q, content=\"\") then call save_plan_append "+
				"repeatedly with chunks up to %d bytes each. "+
				"The cap is the only constraint — chunks well below the cap waste iterations.",
			len(args.Content), maxWriteContentBytes, hint, maxWriteContentBytes))), nil
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		name = fmt.Sprintf("plan-%s", time.Now().Format("20060102-1504"))
	}
	if strings.ContainsAny(name, "/\\") {
		return tools.Failure(call.ID, tools.Validation("save_plan", fmt.Sprintf(
			"name %q must not contain slashes — files always land in .zarlcode/plans/",
			name))), nil
	}
	if !safePlanName.MatchString(name) {
		return tools.Failure(call.ID, tools.Validation("save_plan", fmt.Sprintf(
			"name %q must match [a-z0-9][a-z0-9_-]{0,63} "+
				"(lowercase letters, digits, dash, underscore; no spaces, no dots)",
			name))), nil
	}

	rel := filepath.Join(PlansDir, name+".md")
	abs, err := t.ws.Resolve(rel)
	if err != nil {
		// Resolve fails when the parent doesn't exist (evalExisting
		// follows symlinks on existing paths only). Fall back to a
		// plain join with a contains-root check; we'll mkdir the
		// parent before writing.
		abs = filepath.Join(t.ws.Root(), rel)
		if !strings.HasPrefix(abs, t.ws.Root()) {
			return tools.Failure(call.ID, tools.Permission("save_plan", fmt.Sprintf("resolve %q: %v", rel, err))), nil
		}
	}

	unlock := t.ws.LockPath(abs)
	defer unlock()
	if err := t.ws.WriteFileInRoot(abs, []byte(args.Content), filesystem.ModePublicFile); err != nil {
		return tools.Failure(call.ID, tools.Fatal("save_plan", fmt.Errorf("write %q: %w", rel, err))), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("saved plan to %s (%d bytes)", rel, len(args.Content))), nil
}
