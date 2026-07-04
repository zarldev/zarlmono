package dynamic

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ToolNameBuildTool is the agent-facing tool that turns a Go source
// directory into a built+registered dynamic tool in one call.
const ToolNameBuildTool tools.ToolName = "build_tool"

// BuildTool runs the build-and-register cycle for a directory
// containing a `main.go` written against pkg/ai/tools/toolkit.
//
// Tools resolve the toolkit import via the workspace go.mod's
// `replace github.com/zarldev/zarlmono => ~/.zarlcode/monorepo-stub`
// directive that zarlcode scaffolds on first launch. The
// workspace is the module root; per-tool directories are sub-paths
// of it and must NOT have their own go.mod.
type BuildTool struct {
	registrar *Registrar
	wsRoot    string // workspace root used as cwd parent + sandbox boundary
}

// BuildToolArgs is the typed argument struct BuildTool.Execute
// decodes into via tools.DecodeArgs.
type BuildToolArgs struct {
	Directory string `json:"directory" doc:"Workspace-relative or absolute path to the directory containing main.go."`
}

// NewBuildTool returns the tool that compiles a toolkit-convention Go
// directory under wsRoot and registers the binary with r.
func NewBuildTool(r *Registrar, wsRoot string) *BuildTool {
	return &BuildTool{registrar: r, wsRoot: wsRoot}
}

// Definition advertises build_tool with a single required directory
// parameter (workspace-relative or absolute, must contain main.go).
// Declares Mutates:true — compiling and registering a binary is a
// durable registry change, so it's gated out of read-only spawn modes.
func (t *BuildTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameBuildTool,
		Description: "Compile a Go directory written against pkg/ai/tools/toolkit " +
			"and register the resulting binary in one step. Pass the directory " +
			"path (e.g. tools/sha256_hex). The directory MUST NOT contain a go.mod — " +
			"tools resolve the toolkit through the workspace's root go.mod (which " +
			"zarlcode scaffolds on launch with a replace directive pointing at " +
			"the embedded toolkit stub). Returns the registered name on success. " +
			"Authoring flow: 1) write tools/<name>/main.go using the toolkit; " +
			"2) call build_tool with directory=tools/<name>; that's it.",
		Parameters: tools.SchemaFor[BuildToolArgs](),
		// Compiles a binary and mutates the tool registry — durable state
		// change, so it's gated out of read-only spawn modes.
		Mutates: true,
	}
}

func pathInside(root, target string) (string, bool) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", false
	}
	return rel, true
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Execute resolves directory through EvalSymlinks and rejects anything
// outside the workspace root, requires main.go plus a workspace go.mod,
// runs `go build` in the directory (never `go mod tidy` — the workspace
// go.mod stays untouched), then validates the binary's --describe spec
// and registers it with the registrar.
func (t *BuildTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[BuildToolArgs](call.Arguments)
	if derr != nil {
		return failureResult(call.ID, derr.Error()), nil
	}
	dir := args.Directory
	if dir == "" {
		return failureResult(call.ID, "build_tool: directory required"), nil
	}

	// Resolve to an absolute, real path inside the workspace.
	abs := dir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(t.wsRoot, dir)
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return failureResult(call.ID, fmt.Sprintf("resolve %q: %v", dir, err)), nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return failureResult(call.ID, fmt.Sprintf("stat %q: %v", abs, err)), nil
	}
	if !info.IsDir() {
		return failureResult(call.ID, fmt.Sprintf("%q is not a directory", abs)), nil
	}
	// Workspace boundary: resolve both root and target through
	// EvalSymlinks so a symlink-to-/etc inside the workspace can't
	// trick the path test, then require the real target to live
	// under the real root. Earlier the resolver accepted any
	// absolute path (or `../` traversal) — once `go build` ran in
	// that directory, the build/execute capability escaped the
	// workspace boundary to wherever the OS user could reach.
	realRoot, err := filepath.EvalSymlinks(t.wsRoot)
	if err != nil {
		return failureResult(call.ID, fmt.Sprintf("resolve workspace root %q: %v", t.wsRoot, err)), nil
	}
	realAbs, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return failureResult(call.ID, fmt.Sprintf("resolve %q: %v", abs, err)), nil
	}
	rel, inside := pathInside(realRoot, realAbs)
	if !inside || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: %q resolves outside the workspace (%s). Dynamic "+
				"tools must live under the workspace root; symlink + ../ "+
				"escapes are rejected.", dir, t.wsRoot)), nil
	}
	abs = realAbs

	// main.go must exist.
	mainPath := filepath.Join(abs, "main.go")
	if !pathExists(mainPath) {
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: %s/main.go not found. Write it first using the toolkit.", abs)), nil
	}

	// Workspace go.mod is REQUIRED for dynamic tools — `go mod tidy`
	// walks it to resolve the toolkit import. Used to be auto-
	// scaffolded at launch; that surprised users (every directory got
	// a stray go.mod), so the consumer no longer creates one
	// implicitly. Fail loudly with an actionable hint instead — the
	// user runs `go mod init` once and dynamic-tool authoring works
	// from then on.
	if !pathExists(filepath.Join(t.wsRoot, "go.mod")) {
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: no go.mod in workspace %s. Dynamic tools need a "+
				"Go module to compile against — run `go mod init <name>` in "+
				"the workspace root once, then retry.", t.wsRoot)), nil
	}

	// Build to ./<dirname> inside the directory.
	//
	// We deliberately do NOT run `go mod tidy` first — earlier shapes
	// did, which silently mutated the workspace go.mod / go.sum every
	// time a tool was built. Hidden repo mutations make diffs noisy and
	// can mask real dependency changes a user is mid-flight on. `go
	// build` either finds deps already in go.sum (cached path) or
	// fails with a precise "missing go.sum entry" / "module not found"
	// error that points the user at the explicit fix (`go mod tidy`
	// or `go get`), which is what they want anyway.
	binName := filepath.Base(abs)
	binPath := filepath.Join(abs, binName)
	build := exec.CommandContext(ctx, "go", "build", "-o", binName, ".")
	build.Dir = abs
	var bout bytes.Buffer
	build.Stdout = &bout
	build.Stderr = &bout
	if err := build.Run(); err != nil {
		// Surface missing-module / missing-go.sum errors with an
		// actionable hint — these are the cases users would previously
		// have leaned on the implicit tidy for.
		hint := ""
		out := bout.String()
		if strings.Contains(out, "missing go.sum entry") ||
			strings.Contains(out, "no required module provides") ||
			strings.Contains(out, "cannot find module") {
			hint = "\n(missing deps — run `go mod tidy` in the workspace root, then retry. " +
				"build_tool no longer runs tidy automatically so the workspace go.mod " +
				"stays under your control.)"
		}
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: go build failed: %v\n%s%s", err, out, hint)), nil
	}

	// Ask the binary who it is.
	spec, err := DescribeBinary(ctx, binPath, DefaultCallTimeout)
	if err != nil {
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: %s built but --describe failed: %v", binPath, err)), nil
	}
	if !validToolName.MatchString(string(spec.Name)) {
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: binary reports invalid name %q (must match ^[a-z][a-z0-9_]{1,63}$)", spec.Name)), nil
	}
	if spec.Description == "" {
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: binary %q reports empty description", spec.Name)), nil
	}
	if spec.Parameters.IsZero() {
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: binary %q reports empty parameters schema", spec.Name)), nil
	}
	if err := t.registrar.RegisterContext(ctx, spec, binPath); err != nil {
		return failureResult(call.ID, fmt.Sprintf(
			"build_tool: register %s: %v", binPath, err)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "registered %s as %q (%s)\n", binPath, spec.Name, spec.Description)
	return tools.Success(call.ID, b.String()), nil
}
