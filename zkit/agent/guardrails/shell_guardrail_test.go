package guardrails_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func bashCall(cmd string) tools.ToolCall {
	args := tools.ToolParameters{}
	if cmd != "" {
		args["command"] = cmd
	}
	return tools.ToolCall{ID: "c", ToolName: "bash", Arguments: args}
}

func TestShellGuardrail_SafeCommandPasses(t *testing.T) {
	t.Parallel()
	g := guardrails.NewShellGuardrail("bash")
	if err := g.Before(t.Context(), bashCall("ls -la")); err != nil {
		t.Errorf("safe command: want pass, got %v", err)
	}
}

func TestShellGuardrail_CdRejectsWithGuidance(t *testing.T) {
	t.Parallel()
	g := guardrails.NewShellGuardrail("bash")
	err := g.Before(t.Context(), bashCall("cd /tmp"))
	if err == nil {
		t.Fatal("cd: want Validation rejection")
	}
	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if e.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want Validation", e.Kind)
	}
	for _, want := range []string{"cd", "workspace"} {
		if !strings.Contains(e.Reason, want) {
			t.Errorf("Reason missing %q: %q", want, e.Reason)
		}
	}
}

func TestShellGuardrail_RedirectRejectsWithGuidance(t *testing.T) {
	t.Parallel()
	g := guardrails.NewShellGuardrail("bash")
	err := g.Before(t.Context(), bashCall("echo hi > /tmp/x"))
	if err == nil {
		t.Fatal("redirect: want Validation rejection")
	}
	e, _ := errors.AsType[*tools.Error](err)
	if e == nil || e.Kind != tools.Kinds.VALIDATION {
		t.Fatalf("err = %v, want Validation", err)
	}
	if !strings.Contains(e.Reason, "`write`") {
		t.Errorf("Reason should suggest the write tool: %q", e.Reason)
	}
}

func TestShellGuardrail_OtherToolsUntouched(t *testing.T) {
	t.Parallel()
	g := guardrails.NewShellGuardrail("bash")
	call := tools.ToolCall{
		ID:        "c",
		ToolName:  "read",
		Arguments: tools.ToolParameters{"command": "cd /tmp"}, // even if it had a `command` arg
	}
	if err := g.Before(t.Context(), call); err != nil {
		t.Errorf("non-bash tool: want pass, got %v", err)
	}
}

func TestShellGuardrail_EmptyCommandPassesThrough(t *testing.T) {
	t.Parallel()
	// Empty command is the bash tool's own validation surface; the
	// guardrail should defer to it rather than producing a confusing
	// shell-policy message.
	g := guardrails.NewShellGuardrail("bash")
	if err := g.Before(t.Context(), bashCall("")); err != nil {
		t.Errorf("empty command: want pass-through, got %v", err)
	}
}

func TestShellGuardrail_SyntaxErrorRejects(t *testing.T) {
	t.Parallel()
	g := guardrails.NewShellGuardrail("bash")
	err := g.Before(t.Context(), bashCall("echo 'unterminated"))
	if err == nil {
		t.Fatal("syntax error: want rejection")
	}
	e, _ := errors.AsType[*tools.Error](err)
	if e == nil || e.Kind != tools.Kinds.VALIDATION {
		t.Fatalf("err = %v, want Validation", err)
	}
}
