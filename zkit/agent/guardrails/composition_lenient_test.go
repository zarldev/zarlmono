package guardrails_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// findPre returns the named guardrail from the composed set as a PreCall.
func findPre(t *testing.T, guards []guardrails.Guardrail, name string) guardrails.PreCall {
	t.Helper()
	for _, g := range guards {
		if g.Name() != name {
			continue
		}
		pre, ok := g.(guardrails.PreCall)
		if !ok {
			t.Fatalf("guardrail %q does not implement PreCall", name)
		}
		return pre
	}
	t.Fatalf("guardrail %q not in composed set", name)
	return nil
}

// With the sandbox off (ShellLenient), the composed shell guardrail lets a
// redirect through and a strict read-before-write is softened to advisory —
// it nudges (with an "advisory:" prefix) rather than blocking.
func TestPostSchema_LenientRelaxesShellAndReadBeforeWrite(t *testing.T) {
	t.Parallel()
	deps := guardrails.Deps{
		ReadBeforeWriteMode: guardrails.ReadBeforeWriteStrict,
		ExtraEvidence:       runner.NewMemoryTaskCallLedger(),
		ShellLenient:        true,
	}
	guards := guardrails.PostSchemaGuardrails(deps)

	shell := findPre(t, guards, "shell_policy")
	if err := shell.Before(t.Context(), bashCall("echo hi > /tmp/x")); err != nil {
		t.Errorf("lenient shell: redirect should pass, got %v", err)
	}

	rbw := findPre(t, guards, "read_before_write")
	err := rbw.Before(t.Context(), tools.ToolCall{
		ToolName:  code.ToolNameEdit,
		Arguments: tools.ToolParameters{"path": "pkg/foo.go"},
	})
	if err == nil || !strings.Contains(err.Error(), "advisory:") {
		t.Errorf("lenient read_before_write: want advisory nudge, got %v", err)
	}
}

// With the sandbox on (default strict), the redirect blocks and strict
// read-before-write rejects without the advisory prefix.
func TestPostSchema_StrictKeepsShellAndReadBeforeWrite(t *testing.T) {
	t.Parallel()
	deps := guardrails.Deps{
		ReadBeforeWriteMode: guardrails.ReadBeforeWriteStrict,
		ExtraEvidence:       runner.NewMemoryTaskCallLedger(),
		ShellLenient:        false,
	}
	guards := guardrails.PostSchemaGuardrails(deps)

	shell := findPre(t, guards, "shell_policy")
	if err := shell.Before(t.Context(), bashCall("echo hi > /tmp/x")); err == nil {
		t.Error("strict shell: redirect should block")
	}

	rbw := findPre(t, guards, "read_before_write")
	err := rbw.Before(t.Context(), tools.ToolCall{
		ToolName:  code.ToolNameEdit,
		Arguments: tools.ToolParameters{"path": "pkg/foo.go"},
	})
	if err == nil {
		t.Fatal("strict read_before_write: blind edit should reject")
	}
	if strings.Contains(err.Error(), "advisory:") {
		t.Errorf("strict read_before_write: should be a hard reject, got advisory: %v", err)
	}
}
