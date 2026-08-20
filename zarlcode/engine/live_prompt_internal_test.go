package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestLivePromptFuncMatchesInspectorResolution(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	zarlDir := filepath.Join(homeDir, ".zarlcode")
	mustWrite(t, filepath.Join(zarlDir, home.PreferencesFile), "Prefer terse prompt tests.")

	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := NewLiveRunner(nil, ws, "local")

	assertRuntimeAndInspectorPromptMatch(t, live, false, "BUILD MODE", "# Contract", "Prefer terse prompt tests.")
	if got := live.Inspect(t.Context()).PromptSource; got != "embedded compact system prompt" {
		t.Fatalf("default PromptSource = %q, want compact source", got)
	}
	live.SetPlanMode(true)
	assertRuntimeAndInspectorPromptMatch(t, live, true, "PLAN mode", "Prefer terse prompt tests.")
}

func TestCompactPromptProfileMatchesInspectorAndPreservesOverrides(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	mustWrite(t, filepath.Join(homeDir, ".zarlcode", home.PreferencesFile), "Prefer compact prompts.")
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := NewLiveRunner(nil, ws, "local")

	assertRuntimeAndInspectorPromptMatch(t, live, false, "# Contract", "BUILD MODE")
	if got := live.Inspect(t.Context()).PromptSource; got != "embedded compact system prompt" {
		t.Fatalf("PromptSource = %q, want compact source", got)
	}

	override := "Override for {{.WorkspaceRoot}}."
	mustWrite(t, filepath.Join(homeDir, ".zarlcode", home.PromptOverrideFile), override)
	ins := live.Inspect(t.Context())
	if !strings.Contains(ins.PromptSystem, "Override for "+ws.Root()) {
		t.Fatalf("compact profile bypassed explicit override:\n%s", ins.PromptSystem)
	}
	if ins.PromptResolutionMode != home.PromptExplicitOverride {
		t.Fatalf("PromptResolutionMode = %s, want explicit override", ins.PromptResolutionMode)
	}
}

func TestStandardPromptProfileRemainsRollback(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := NewLiveRunner(nil, ws, "local", WithPromptProfile(PromptProfiles.STANDARD))
	ins := live.Inspect(t.Context())
	if ins.PromptSource != "embedded system prompt" {
		t.Fatalf("PromptSource = %q, want standard rollback source", ins.PromptSource)
	}
	if !strings.Contains(ins.PromptSystem, "# Working style") {
		t.Fatalf("standard rollback prompt not selected:\n%s", ins.PromptSystem)
	}
}

func assertRuntimeAndInspectorPromptMatch(t *testing.T, live *LiveRunner, plan bool, wants ...string) {
	t.Helper()
	ctx := t.Context()
	src, reg, err := live.source(t.Context(), nil)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	visible := NewModeFilteredSource(src, live.isPlan)
	promptFn := live.promptFunc(func() tools.Source { return visible })
	got, err := promptFn(ctx, runner.PromptVars{})
	if err != nil {
		t.Fatalf("promptFunc: %v", err)
	}
	r := runner.New(inspectorClient{}, runner.WithTools(visible), runner.WithPrompt(runner.StaticPrompt("")), runner.WithSink(runner.NopSink{}))
	live.registerSpawnTools(ctx, reg, r, spawn.NewGroup(), nil, 0, 0)
	ins := live.Inspect(ctx)
	if ins.PlanMode != plan {
		t.Fatalf("Inspect PlanMode = %v, want %v", ins.PlanMode, plan)
	}
	if got != ins.PromptSystem {
		t.Fatalf("runtime prompt and inspector prompt differ")
	}
	if ins.PromptResolutionMode != home.PromptEmbeddedCore {
		t.Fatalf("PromptResolutionMode = %s, want %s", ins.PromptResolutionMode, home.PromptEmbeddedCore)
	}
	if ins.PromptPreferencesSource == "" {
		t.Fatal("inspector did not record preferences source")
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}
