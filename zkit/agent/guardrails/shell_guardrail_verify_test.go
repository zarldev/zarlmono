package guardrails_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// The shell guardrail applies the verify profile only when the ctx carries
// the verify work mode (planted by the spawn tool on a verify-mode child
// Run): the same mutating command passes for an unscoped run and blocks
// for a verify one, while test commands pass in both.
func TestShellGuardrail_VerifyModeFromContext(t *testing.T) {
	g := guardrails.NewShellGuardrail("bash")
	bashCall := func(cmd string) tools.ToolCall {
		return tools.ToolCall{ID: "c1", ToolName: "bash", Arguments: tools.ToolParameters{"command": cmd}}
	}
	verifyCtx := taskscope.WithWorkMode(t.Context(), taskscope.WorkModes.VERIFY)

	if err := g.Before(t.Context(), bashCall("rm -rf build")); err != nil {
		t.Errorf("unscoped run: rm should pass the standard rules, got %v", err)
	}
	err := g.Before(verifyCtx, bashCall("rm -rf build"))
	if err == nil {
		t.Fatal("verify run: rm should be blocked")
	}
	if !strings.Contains(err.Error(), "verify") {
		t.Errorf("rejection should name the verify profile, got %q", err.Error())
	}
	if err := g.Before(verifyCtx, bashCall("go test ./...")); err != nil {
		t.Errorf("verify run: go test should pass, got %v", err)
	}
	// Standard rules still hold in verify mode.
	if err := g.Before(verifyCtx, bashCall("cd /tmp")); err == nil {
		t.Error("verify run: cd should stay blocked by the standard rules")
	}
}
