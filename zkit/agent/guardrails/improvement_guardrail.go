package guardrails

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Verifier runs a fast compile / typecheck / lint pass against a set
// of paths and reports any diagnostics as an error. Implementations
// are language-keyed via Extensions; ImprovementGuardrail routes a
// tool call's touched files to whichever verifier claims the right
// suffix.
//
// "Fast" is the load-bearing word — verifiers run on every successful
// mutation of a watched tool, so they live inside the agent's turn.
// Anything slower than a few seconds belongs in a separate guardrail
// (or in SWE-bench's evaluator, which runs the full test suite post-
// hoc anyway).
type Verifier interface {
	// Name identifies the verifier in error messages.
	Name() string
	// Extensions lists the file suffixes this verifier handles
	// (".go", ".ts", etc.). ImprovementGuardrail uses these to route.
	Extensions() []string
	// Verify runs the check against paths under root. Returns nil on
	// success; a non-nil error's Error() is shown to the LLM as the
	// validation reason.
	Verify(ctx context.Context, root string, paths []string) error
}

// ImprovementGuardrail is a PostCall guardrail that turns "the tool
// said it succeeded" into "the tool said it succeeded AND the code it
// touched still compiles". After every successful mutation of a
// watched tool (write, edit, write_append by default), or after any
// successful tool result that carries file effects, the guardrail extracts
// the touched paths, groups them by extension, and asks the matching verifier
// whether the workspace is still healthy. A failed verification does NOT
// revert or fail the call — like the rest of the zarlcode guardrails it is
// advisory: it annotates the (still-successful) result with the diagnostic so
// the model sees it and can react, without hard-stopping a multi-edit refactor
// that legitimately passes through intermediate non-compiling states.
//
// The improvement loop only fires the IN-LOOP verifier (fast: vet,
// typecheck, lint). Full test runs are out of scope here — they're
// what SWE-bench's evaluator does at scoring time and they're too
// expensive to run on every edit.
type ImprovementGuardrail struct {
	root       string
	byExt      map[string]Verifier
	watchTools map[tools.ToolName]struct{}
}

// NewImprovementGuardrail wires up a guardrail that watches the named
// tools and dispatches their touched paths to the supplied verifiers.
// Empty watch list defaults to {"write", "edit", "write_append"} —
// the legacy path-bearing mutators whose arguments can be inspected when a
// result carries no typed effects. Tools that emit file effects (for example
// apply_patch) are verified from those effects regardless of this watch list.
func NewImprovementGuardrail(root string, watch []tools.ToolName, verifiers ...Verifier) *ImprovementGuardrail {
	if len(watch) == 0 {
		watch = []tools.ToolName{code.ToolNameWrite, code.ToolNameEdit, code.ToolNameWriteAppend}
	}
	byExt := make(map[string]Verifier)
	for _, v := range verifiers {
		for _, e := range v.Extensions() {
			byExt[e] = v
		}
	}
	watchSet := make(map[tools.ToolName]struct{}, len(watch))
	for _, n := range watch {
		watchSet[n] = struct{}{}
	}
	return &ImprovementGuardrail{
		root:       root,
		byExt:      byExt,
		watchTools: watchSet,
	}
}

// Name returns the guardrail's identifier.
func (g *ImprovementGuardrail) Name() string { return "improvement_loop" }

// Inspect runs the in-loop verification. Always returns nil — the
// guardrail never fails or reverts the call. When a verifier reports
// diagnostics it appends an advisory to the (still-successful) result
// so the model sees the breakage without being hard-stopped mid-
// refactor. Does nothing when the tool didn't mutate, the path's
// extension isn't watched, or every verifier passes.
func (g *ImprovementGuardrail) Inspect(
	ctx context.Context,
	call tools.ToolCall,
	result *tools.ToolResult,
	dispatchErr error,
) error {
	if !successfulResult(result, dispatchErr) {
		return nil
	}
	paths := effectModifiedPaths(result)
	if len(paths) == 0 {
		if _, watched := g.watchTools[call.ToolName]; !watched {
			return nil
		}
		paths = touchedPaths(call)
	}
	if len(paths) == 0 {
		return nil
	}
	// Group paths by which verifier owns them. Files with extensions
	// no verifier handles are skipped silently — README touches don't
	// need an advisory because no Markdown verifier is wired.
	byVerifier := map[Verifier][]string{}
	for _, p := range paths {
		v, ok := g.byExt[filepath.Ext(p)]
		if !ok {
			continue
		}
		byVerifier[v] = append(byVerifier[v], p)
	}
	var diags []string
	for v, vpaths := range byVerifier {
		if err := v.Verify(ctx, g.root, vpaths); err != nil {
			diags = append(diags, fmt.Sprintf("%s: %s", v.Name(), err.Error()))
		}
	}
	if len(diags) > 0 {
		annotateResult(result, fmt.Sprintf(
			"advisory: the edit applied, but in-loop verification of the code it "+
				"touched still reports problems:\n%s\n"+
				"The edit was NOT reverted. If you're mid-refactor this may clear "+
				"once the rest of your edits land; otherwise fix it before moving on.",
			strings.Join(diags, "\n")))
	}
	return nil
}

// touchedPaths extracts the file path(s) a tool mutated from its
// call arguments. Only path-bearing tools are handled here; tools
// without an explicit "path" arg (apply_patch, bash) return nil and
// the guardrail skips them.
func touchedPaths(call tools.ToolCall) []string {
	if p := call.Arguments.String("path", ""); p != "" {
		return []string{p}
	}
	return nil
}

func effectModifiedPaths(result *tools.ToolResult) []string {
	if result == nil {
		return nil
	}
	var paths []string
	for _, e := range result.FileEffects() {
		switch e.Op {
		case tools.FileCreate, tools.FileModify, tools.FileAppend, tools.FileRename:
			if e.Path != "" {
				paths = append(paths, e.Path)
			}
		}
	}
	return paths
}
