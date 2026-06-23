package guardrails

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// TestEditAdvisoryGuardrail is a soft post-call advisor that fires when the
// agent successfully modifies a test file. It does not reject the edit; it
// annotates the result so the model can reconsider whether the test edit masked
// a production bug.
type TestEditAdvisoryGuardrail struct {
	tools []tools.ToolName
}

var defaultTestEditTools = map[tools.ToolName]struct{}{
	code.ToolNameWrite:       {},
	code.ToolNameEdit:        {},
	code.ToolNameWriteAppend: {},
	code.ToolNameApplyPatch:  {},
}

// NewTestEditAdvisory builds the interactive-mode test edit advisory. With no
// tool names it watches the default write-style tools.
func NewTestEditAdvisory(names ...tools.ToolName) *TestEditAdvisoryGuardrail {
	return &TestEditAdvisoryGuardrail{tools: names}
}

// Name returns the guardrail identifier.
func (g *TestEditAdvisoryGuardrail) Name() string { return "test_edit_advisory" }

// Inspect appends an advisory to the result when a watched tool successfully
// edits a path that looks like a test file.
func (g *TestEditAdvisoryGuardrail) Inspect(
	_ context.Context,
	call tools.ToolCall,
	result *tools.ToolResult,
	dispatchErr error,
) error {
	if !successfulResult(result, dispatchErr) {
		return nil
	}
	if !matchesTestEditTool(g.tools, call.ToolName) {
		return nil
	}
	path := firstMatchingPath(testEditTouchedPaths(call), looksLikeTestFile)
	if path == "" {
		return nil
	}
	annotateResult(result, fmt.Sprintf(
		"advisory: this call modified %q which looks like a test file. "+
			"Test edits are sometimes correct, but they're a common way to "+
			"mask a bug — production code is changed to behave one way, then "+
			"the test is 'fixed' to expect that behavior. Before continuing, "+
			"check whether the production-code change is actually right; if "+
			"so, the test change is correct; if not, revert this edit and "+
			"reconsider the production change.",
		path))
	return nil
}

// TestEditStrictGuardrail is a headless/eval-mode pre-call rejector for test
// files and fixtures. It prevents source diffs from being contaminated by
// agent-authored tests when an external grader supplies its own tests.
type TestEditStrictGuardrail struct {
	tools []tools.ToolName
}

// NewTestEditStrict builds the headless-mode test edit guardrail. With no tool
// names it watches the default write-style tools.
func NewTestEditStrict(names ...tools.ToolName) *TestEditStrictGuardrail {
	return &TestEditStrictGuardrail{tools: names}
}

// Name returns the guardrail identifier.
func (g *TestEditStrictGuardrail) Name() string { return "test_edit_strict" }

// Before rejects any write-style call against a path that looks like a test
// file or test fixture. The bash tool is screened separately — it is the one
// registered tool that can mutate a path without naming it in a "path"
// argument, so without this it is a free bypass of the write-style guards (a
// model can `sed -i`, `rm`, or `> ` a grader's test file and the file-tool
// branch never sees it).
func (g *TestEditStrictGuardrail) Before(_ context.Context, call tools.ToolCall) error {
	if call.ToolName == code.ToolNameBash {
		return g.checkBash(call)
	}
	if !matchesTestEditTool(g.tools, call.ToolName) {
		return nil
	}
	path := firstMatchingPath(testEditTouchedPaths(call), looksLikeTestPath)
	if path == "" {
		return nil
	}
	return tools.Validation(string(call.ToolName), fmt.Sprintf(
		"refused: %q is a test file or test fixture, and this run is "+
			"unattended (headless / eval mode). The grader provides its "+
			"own test patch — anything you write here will either "+
			"conflict with the grader's expectations or be ignored, "+
			"scoring zero. Modify SOURCE files only. If you wanted to "+
			"demonstrate the fix, the production-code change is enough — "+
			"the grader will run its own tests against it.",
		path))
}

// checkBash rejects a bash command that would write to or delete a test file
// or fixture. Targets are extracted statically via shellpolicy.WriteTargets
// (no execution). An unparseable command is left to the ShellGuardrail, which
// fails it closed on the syntax error separately, so we return nil here rather
// than guessing.
func (g *TestEditStrictGuardrail) checkBash(call tools.ToolCall) error {
	command := call.Arguments.String("command", "")
	if command == "" {
		return nil
	}
	targets, err := shellpolicy.WriteTargets(command)
	if errors.Is(err, shellpolicy.ErrUnparseable) {
		// The ShellGuardrail's syntax check rejects an unparseable command
		// closed separately, so don't guess at targets or double-handle it.
		return nil
	}
	path := firstMatchingPath(targets, looksLikeTestPath)
	if path == "" {
		// Static path analysis can't see inside an interpreter's inline
		// code (`python -c "open('foo_test.go','w')"`), so scan those
		// payloads for a protected path and fail closed when one appears.
		path = firstInterpreterTestPath(command)
	}
	if path == "" {
		return nil
	}
	return tools.Validation(string(call.ToolName), fmt.Sprintf(
		"refused: this command writes to or deletes %q, which is a test "+
			"file or test fixture, and this run is unattended (headless / "+
			"eval mode). The grader provides its own test patch — modifying "+
			"or removing its tests scores zero. Use bash for builds, reads, "+
			"and running tests only; make your fix in SOURCE files.",
		path))
}

// firstInterpreterTestPath returns the first protected test path mentioned in
// an interpreter inline-code payload (python -c / node -e / sh -c …), or "" if
// none. Tokens are split on characters that can't appear in a path so a path
// embedded in a quoted string literal is still recognised.
func firstInterpreterTestPath(command string) string {
	codes, err := shellpolicy.InterpreterInlineCode(command)
	if err != nil {
		return ""
	}
	for _, code := range codes {
		tokens := strings.FieldsFunc(code, func(r rune) bool {
			return r != '.' && r != '_' && r != '/' && r != '-' &&
				(r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
		})
		if p := firstMatchingPath(tokens, looksLikeTestPath); p != "" {
			return p
		}
	}
	return ""
}

func matchesTestEditTool(names []tools.ToolName, name tools.ToolName) bool {
	if len(names) == 0 {
		_, ok := defaultTestEditTools[name]
		return ok
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func firstMatchingPath(paths []string, matches func(string) bool) string {
	for _, path := range paths {
		if path != "" && matches(path) {
			return path
		}
	}
	return ""
}
