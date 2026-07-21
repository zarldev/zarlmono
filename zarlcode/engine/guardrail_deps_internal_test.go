package engine

import (
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/prefs"
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

// The interactive test-edit policy now follows the test_edit_guard setting
// (off → none, advisory → advisory, strict → strict); headless stays strict
// regardless so eval grading tests can't be quietly weakened.
func TestInteractiveTestEditModeFollowsSetting(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	s := newJudgeTestSettings(t)
	live := NewLiveRunner(nil, ws, nil, "local")
	live.SetSettingsHandle(s)

	set := func(val string) {
		t.Helper()
		if err := s.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyTestEditGuard, val); err != nil {
			t.Fatalf("set: %v", err)
		}
	}

	set("advisory")
	if g := live.guardrailDeps().TestEdit; g == nil || g.Name() != "test_edit_advisory" {
		t.Fatalf("advisory setting → %v, want test_edit_advisory", g)
	}
	set("strict")
	if g := live.guardrailDeps().TestEdit; g == nil || g.Name() != "test_edit_strict" {
		t.Fatalf("strict setting → %v, want test_edit_strict", g)
	}
	set("off")
	if g := live.guardrailDeps().TestEdit; g != nil {
		t.Fatalf("off setting → %q, want no guardrail", g.Name())
	}
	// Headless ignores the setting and stays strict.
	if name := live.headlessGuardrailDeps().TestEdit.Name(); name != "test_edit_strict" {
		t.Fatalf("headless test-edit = %q, want test_edit_strict", name)
	}
}

// Turning the always-on guardrails off drops them from the chain via Disabled.
func TestImprovementAndSkillHintsDisableViaSettings(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	s := newJudgeTestSettings(t)
	live := NewLiveRunner(nil, ws, nil, "local")
	live.SetSettingsHandle(s)

	// On by default: nothing disabled.
	if d := live.guardrailDeps().Disabled; len(d) != 0 {
		t.Fatalf("default Disabled = %v, want empty", d)
	}

	for _, kv := range []struct{ key, name string }{
		{prefs.KeyImprovementGuard, guardrails.NameImprovementLoop},
		{prefs.KeySkillHints, guardrails.NameSkillHint},
	} {
		if err := s.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, kv.key, "off"); err != nil {
			t.Fatalf("set %s: %v", kv.key, err)
		}
	}
	got := live.guardrailDeps().Disabled
	for _, want := range []string{guardrails.NameImprovementLoop, guardrails.NameSkillHint} {
		if !slices.Contains(got, want) {
			t.Fatalf("Disabled = %v, want to contain %q", got, want)
		}
	}
}

// shell_guard "auto" follows the sandbox; strict/lenient pin the choice.
func TestShellGuardLenientModes(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	s := newJudgeTestSettings(t)
	live := NewLiveRunner(nil, ws, nil, "local")
	live.SetSettingsHandle(s)

	setSandbox := func(v string) {
		t.Helper()
		if err := s.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeySandbox, v); err != nil {
			t.Fatalf("set sandbox: %v", err)
		}
	}
	setGuard := func(v string) {
		t.Helper()
		if err := s.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyShellGuard, v); err != nil {
			t.Fatalf("set shell_guard: %v", err)
		}
	}

	// auto: lenient only when the sandbox is off.
	setSandbox("on")
	if live.guardrailDeps().ShellLenient {
		t.Fatal("auto + sandbox on → want strict (ShellLenient false)")
	}
	setSandbox("off")
	if !live.guardrailDeps().ShellLenient {
		t.Fatal("auto + sandbox off → want lenient (ShellLenient true)")
	}
	// Pinned modes ignore the sandbox.
	setGuard("strict")
	setSandbox("off")
	if live.guardrailDeps().ShellLenient {
		t.Fatal("strict pin → want ShellLenient false regardless of sandbox")
	}
	setGuard("lenient")
	setSandbox("on")
	if !live.guardrailDeps().ShellLenient {
		t.Fatal("lenient pin → want ShellLenient true regardless of sandbox")
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
