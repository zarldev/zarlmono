package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestLivePromptMatchesInspectorResolution(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	mustWriteFile(t, filepath.Join(homeDir, ".zarlcode", home.PreferencesFile), "Prefer terse prompt tests.")
	live := newLive(t)

	assertInspectionPrompt(t, live, false, "BUILD MODE", "# Contract", "Prefer terse prompt tests.")
	if got := live.Inspect(t.Context()).PromptSource; got != "embedded compact system prompt" {
		t.Fatalf("default PromptSource = %q, want compact source", got)
	}
	live.SetPlanMode(true)
	assertInspectionPrompt(t, live, true, "PLAN mode", "Prefer terse prompt tests.")
}

func TestCompactPromptProfilePreservesOverrides(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	mustWriteFile(t, filepath.Join(homeDir, ".zarlcode", home.PreferencesFile), "Prefer compact prompts.")
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, ws, "local")
	assertInspectionPrompt(t, live, false, "# Contract", "BUILD MODE")

	mustWriteFile(t, filepath.Join(homeDir, ".zarlcode", home.PromptOverrideFile), "Override for {{.WorkspaceRoot}}.")
	ins := live.Inspect(t.Context())
	if !strings.Contains(ins.PromptSystem, "Override for "+ws.Root()) {
		t.Fatalf("compact profile bypassed explicit override:\n%s", ins.PromptSystem)
	}
	if ins.PromptResolutionMode != home.PromptExplicitOverride {
		t.Fatalf("PromptResolutionMode = %s, want explicit override", ins.PromptResolutionMode)
	}
}

func TestStandardPromptProfileRemainsRollback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, ws, "local", engine.WithPromptProfile(engine.PromptProfiles.STANDARD))
	ins := live.Inspect(t.Context())
	if ins.PromptSource != "embedded system prompt" || !strings.Contains(ins.PromptSystem, "# Working style") {
		t.Fatalf("standard rollback prompt not selected: source=%q\n%s", ins.PromptSource, ins.PromptSystem)
	}
}

func assertInspectionPrompt(t *testing.T, live *engine.LiveRunner, plan bool, wants ...string) {
	t.Helper()
	ins := live.Inspect(t.Context())
	if ins.PlanMode != plan {
		t.Fatalf("Inspect PlanMode = %v, want %v", ins.PlanMode, plan)
	}
	if ins.PromptResolutionMode != home.PromptEmbeddedCore {
		t.Fatalf("PromptResolutionMode = %s, want %s", ins.PromptResolutionMode, home.PromptEmbeddedCore)
	}
	if ins.PromptPreferencesSource == "" {
		t.Fatal("inspector did not record preferences source")
	}
	for _, want := range wants {
		if !strings.Contains(ins.PromptSystem, want) {
			t.Fatalf("prompt missing %q:\n%s", want, ins.PromptSystem)
		}
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
