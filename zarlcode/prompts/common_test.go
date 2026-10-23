package prompts_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/prompts"
)

func TestEmbeddedWorkflowsShareStableOperatingPrefix(t *testing.T) {
	for _, programmatic := range []bool{false, true} {
		data := prompts.Data{
			WorkspaceRoot: "/repo", ProgrammaticTools: programmatic,
			Planning: true, CanAuthorTool: true, CanRegisterTool: true,
			UserPreferences: "Prefer concise answers. {{literal}}",
			InstructionDocs: []prompts.InstructionDoc{{Path: "AGENTS.md", Content: "Preserve user work."}},
		}
		var shared string
		for _, workflow := range []struct {
			name, body, mode string
		}{
			{name: "plan", body: prompts.Plan, mode: "**PLAN mode**"},
			{name: "build", body: prompts.System, mode: "**BUILD MODE**"},
			{name: "compact", body: prompts.SystemCompact, mode: "**BUILD MODE**"},
		} {
			out, err := prompts.Render(workflow.name, workflow.body, data)
			if err != nil {
				t.Fatal(err)
			}
			prefix, suffix, found := strings.Cut(out, "# Active workflow\n")
			if !found || len(prefix) < 2000 {
				t.Fatalf("%s has no substantial shared prefix (%d bytes)", workflow.name, len(prefix))
			}
			if shared == "" {
				shared = prefix
			} else if prefix != shared {
				t.Errorf("%s changed the common prefix", workflow.name)
			}
			if strings.Contains(prefix, "**PLAN mode**") || strings.Contains(prefix, "**BUILD MODE**") || !strings.Contains(suffix, workflow.mode) {
				t.Errorf("%s mode is not confined to the workflow suffix", workflow.name)
			}
			for _, want := range []string{"/repo", "Prefer concise answers. {{literal}}", "Preserve user work."} {
				if !strings.Contains(out, want) {
					t.Errorf("%s dropped guidance %q", workflow.name, want)
				}
			}
			if workflow.name == "plan" && !strings.Contains(suffix, "Only read-only investigation and writes to") {
				t.Error("shared prefix weakened Plan restrictions")
			}
		}
	}
}

func TestExplicitPromptOverrideDoesNotAcquireEmbeddedPrefix(t *testing.T) {
	out, err := prompts.Render("override", "User-owned override for {{.WorkspaceRoot}}.", prompts.Data{WorkspaceRoot: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "User-owned override for /repo.\n" {
		t.Fatalf("override changed: %q", out)
	}
}

func TestEmbeddedWorkflowsKeepSharedRulesWithoutSuffixRepetition(t *testing.T) {
	for _, capability := range []string{"none", "author", "register"} {
		for _, programmatic := range []bool{false, true} {
			data := prompts.Data{
				WorkspaceRoot: "/repo", Planning: true, ProgrammaticTools: programmatic,
				CanAuthorTool: capability == "author", CanRegisterTool: capability == "register",
				UserPreferences: "Literal {{preference}}", InstructionDocs: []prompts.InstructionDoc{{Path: "AGENTS.md", Content: "Literal {{instruction}}"}},
			}
			for _, body := range []string{prompts.System, prompts.SystemCompact, prompts.Plan} {
				out, err := prompts.Render("rules", body, data)
				if err != nil {
					t.Fatal(err)
				}
				for _, rule := range []string{
					"Do not make unrelated fixes", "Use web research only", "spill path",
					"promise a next action", "report the result and verification performed",
					"Literal {{preference}}", "Literal {{instruction}}",
				} {
					if count := strings.Count(out, rule); count != 1 {
						t.Errorf("capability=%s programmatic=%v: %q appears %d times, want once", capability, programmatic, rule, count)
					}
				}
			}
		}
	}
}
