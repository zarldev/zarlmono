package prompts_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/prompts"
)

func TestRender_SelfModGating(t *testing.T) {
	base := prompts.Data{
		WorkspaceRoot: "/repo",
		Tools:         []prompts.ToolInfo{{Name: "read", Description: "read a file"}},
	}

	lean, err := prompts.Render("system", prompts.System, base)
	if err != nil {
		t.Fatal(err)
	}
	// Eval / no-self-mod-tools render: none of the self-modification material.
	for _, banned := range []string{"You are zarlcode", "modify your own definition", "Building a new dynamic tool", "new_tool", "go mod init"} {
		if strings.Contains(lean, banned) {
			t.Errorf("lean render should not contain %q (SelfMod=false)", banned)
		}
	}

	rich := base
	rich.SelfMod = true
	full, err := prompts.Render("system", prompts.System, rich)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are zarlcode", "new_tool", "Extending yourself"} {
		if !strings.Contains(full, want) {
			t.Errorf("SelfMod render missing %q", want)
		}
	}
}

func TestRender_PlanningGating(t *testing.T) {
	base := prompts.Data{WorkspaceRoot: "/repo"}

	off, err := prompts.Render("system", prompts.System, base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(off, "update_plan") {
		t.Errorf("Planning=false render should not mention update_plan")
	}

	base.Planning = true
	on, err := prompts.Render("system", prompts.System, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"update_plan",
		"leave the plan truthful before finishing",
		"mark done steps `completed`",
	} {
		if !strings.Contains(on, want) {
			t.Errorf("Planning=true render missing %q", want)
		}
	}
}

func TestRender_CommonOperatingCoreAlwaysPresent(t *testing.T) {
	// The load-bearing operating discipline must render in BOTH the lean
	// (eval) and rich (TUI) shapes — this is the content the anti-drift
	// guarantee is about.
	for _, selfMod := range []bool{false, true} {
		d := prompts.Data{WorkspaceRoot: "/repo", SelfMod: selfMod}
		out, err := prompts.Render("system", prompts.System, d)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"spawn_agent",                // delegation discipline
			"[compacted —",               // compaction-recovery marker
			"spill path",                 // tool-result truncation behaviour
			"through the tool interface", // tools come via the API, not enumerated in the prompt
			"one clear action",           // local-model-friendly call discipline
			"/repo",                      // workspace root interpolation
		} {
			if !strings.Contains(out, want) {
				t.Errorf("SelfMod=%v render missing core content %q", selfMod, want)
			}
		}
	}
}

func TestRender_WorkspaceInstructionsTail(t *testing.T) {
	d := prompts.Data{
		WorkspaceRoot:   "/repo",
		InstructionDocs: []prompts.InstructionDoc{{Path: "AGENTS.md", Content: "Run tests."}},
	}
	out, err := prompts.Render("system", prompts.System, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Workspace instructions", "## AGENTS.md", "Run tests."} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing workspace-instruction content %q", want)
		}
	}
}

func TestRender_LazyContextGuidance(t *testing.T) {
	d := prompts.Data{WorkspaceRoot: "/repo"}
	out, err := prompts.Render("system", prompts.System, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lazy context", "list/load tools", "do not read catalogue bodies", "re-read the changed content"} {
		if !strings.Contains(out, want) {
			t.Errorf("system prompt missing lazy-context guidance %q", want)
		}
	}

	out, err = prompts.Render("plan", prompts.Plan, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lazy context", "list/load tools", "do not read catalogue bodies", "verification"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan prompt missing lazy-context guidance %q", want)
		}
	}
}

func TestRender_ProgrammaticToolsGuidance(t *testing.T) {
	base := prompts.Data{WorkspaceRoot: "/repo"}
	without, err := prompts.Render("system", prompts.System, base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without, "program` replaces") {
		t.Fatal("programmatic guidance rendered without ProgrammaticTools")
	}
	base.ProgrammaticTools = true
	with, err := prompts.Render("system", prompts.System, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"program` replaces", "Keep `bash` for real shell work", "Keep `edit`/`write`"} {
		if !strings.Contains(with, want) {
			t.Errorf("system prompt missing programmatic guidance %q", want)
		}
	}
	plan, err := prompts.Render("plan", prompts.Plan, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"program` replaces", "read-only investigation fan-out", "build/test/git"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan prompt missing programmatic guidance %q", want)
		}
	}
}

func TestHasTool(t *testing.T) {
	tools := []prompts.ToolInfo{{Name: "read"}, {Name: "update_plan"}}
	if !prompts.HasTool(tools, "update_plan") {
		t.Error("HasTool should find update_plan")
	}
	if prompts.HasTool(tools, "new_tool") {
		t.Error("HasTool should not find new_tool")
	}
}
