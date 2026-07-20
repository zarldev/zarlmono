package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
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
	live := NewLiveRunner(nil, ws, nil, "local")

	assertRuntimeAndInspectorPromptMatch(t, live, false, "BUILD MODE", "Prefer terse prompt tests.")
	live.SetPlanMode(true)
	assertRuntimeAndInspectorPromptMatch(t, live, true, "PLAN mode", "Prefer terse prompt tests.")
}

func assertRuntimeAndInspectorPromptMatch(t *testing.T, live *LiveRunner, plan bool, wants ...string) {
	t.Helper()
	ctx := t.Context()
	src, reg, err := live.source("")
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	visible := NewModeFilteredSource(src, live.isPlan)
	promptFn := live.promptFunc(func() tools.Source { return visible })
	got, err := promptFn(ctx, runner.PromptVars{})
	if err != nil {
		t.Fatalf("promptFunc: %v", err)
	}
	r := runner.New(inspectorClient{}, runner.WithTools(visible), runner.WithPrompt(runner.StaticPrompt("")), runner.WithSink(nil))
	live.registerSpawnTool(reg, r, 0, 0)
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
