package engine

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Headless/eval turns harden test-edit to strict (the grader's tests must
// survive untouched); interactive turns have no test-edit guardrail —
// the advisory is an eval tool, not needed when a human is in the loop.
func TestHeadlessGuardrailDepsUseStrictTestEdit(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := NewLiveRunner(nil, ws, nil, "local")

	if name := live.headlessGuardrailDeps().TestEdit.Name(); name != "test_edit_strict" {
		t.Fatalf("headless test-edit policy = %q, want test_edit_strict", name)
	}
	if g := live.guardrailDeps().TestEdit; g != nil {
		t.Fatalf("interactive test-edit policy = %q, want nil (no test-edit guardrail)", g.Name())
	}
}

func TestZarlcodeGuardrailDepsDoNotDefaultLoadGoVerifier(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := NewLiveRunner(nil, ws, nil, "local")

	if got := live.guardrailDeps().Verifiers; len(got) != 0 {
		t.Fatalf("interactive verifiers = %d, want none by default", len(got))
	}
	if got := live.headlessGuardrailDeps().Verifiers; len(got) != 0 {
		t.Fatalf("headless verifiers = %d, want none by default", len(got))
	}
}

func TestStandardFanoutDepsLeaveReadUncapped(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := NewLiveRunner(nil, ws, nil, "local")

	limits := live.guardrailDeps().FanoutLimits
	if _, ok := limits[code.ToolNameRead]; ok {
		t.Fatalf("read fanout cap = %d, want uncapped", limits[code.ToolNameRead])
	}
	for _, name := range []tools.ToolName{code.ToolNameLs, code.ToolNameGrep, code.ToolNameGlob} {
		if limits[name] <= 0 {
			t.Fatalf("%s fanout cap = %d, want positive", name, limits[name])
		}
	}
}
