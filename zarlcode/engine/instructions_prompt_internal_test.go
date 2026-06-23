package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/instructions"
	"github.com/zarldev/zarlmono/zarlcode/prompts"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRenderLivePromptIncludesWorkspaceInstructions(t *testing.T) {
	docs := []instructions.Document{{RelPath: "AGENTS.md", Content: "Always run tests."}}
	prompt, err := RenderLivePrompt("test", LiveSystemPromptTemplate, "/repo", nil, nil, docs, []promptTool{{Name: "read"}})
	if err != nil {
		t.Fatal(err)
	}
	assertPromptContains(t, prompt,
		"# Workspace instructions",
		"repository/workspace guidance",
		"do not override system, developer, tool, or safety instructions",
		"## AGENTS.md",
		"Always run tests.",
	)
}

func TestRenderLivePlanPromptIncludesWorkspaceInstructions(t *testing.T) {
	docs := []instructions.Document{{RelPath: "CLAUDE.md", Content: "Plan before editing."}}
	prompt, err := RenderLivePrompt("plan", LivePlanPromptTemplate, "/repo", nil, nil, docs, []promptTool{{Name: "read"}})
	if err != nil {
		t.Fatal(err)
	}
	assertPromptContains(t, prompt,
		"PLAN mode",
		"# Workspace instructions",
		"## CLAUDE.md",
		"Plan before editing.",
	)
}

func TestRenderAgentPromptIncludesWorkspaceInstructions(t *testing.T) {
	docs := []instructions.Document{{RelPath: "nested/AGENTS.md", Content: "Nested rules apply."}}
	prompt, err := RenderLivePrompt("agent:reviewer", "You review code.", "/repo", nil, nil, docs, []promptTool{{Name: "read"}})
	if err != nil {
		t.Fatal(err)
	}
	assertPromptContains(t, prompt,
		"You review code.",
		"# Workspace instructions",
		"## nested/AGENTS.md",
		"Nested rules apply.",
	)
}

func TestBuildTurnReloadsWorkspaceInstructions(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "First version.")
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	l := NewLiveRunner(nil, ws, nil, "local")
	l.buildTurn()
	assertInstructionSnapshotContains(t, l.instructionSnapshotDocs(), "First version.")

	mustWrite(t, filepath.Join(root, "AGENTS.md"), "Second version.")
	l.buildTurn()
	assertInstructionSnapshotContains(t, l.instructionSnapshotDocs(), "Second version.")
}

func assertPromptContains(t *testing.T, prompt string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func assertInstructionSnapshotContains(t *testing.T, docs []instructions.Document, want string) {
	t.Helper()
	if len(docs) != 1 {
		t.Fatalf("got %d instruction docs, want 1: %#v", len(docs), docs)
	}
	if !strings.Contains(docs[0].Content, want) {
		t.Fatalf("instruction snapshot missing %q: %#v", want, docs[0])
	}
}

func TestBuildPromptStackAccountsFragmentsWithoutChangingPrompt(t *testing.T) {
	docs := []instructions.Document{{RelPath: "AGENTS.md", Content: "Always run tests."}}
	skills := []catalog.Skill{{Name: "go-testing", Body: "Use table tests.", Source: "/skills/go-testing.md"}}
	agents := []catalog.Agent{{Name: "reviewer", Body: "Review code.", Source: "/agents/reviewer.md"}}
	body := "You are an agent."
	rendered, err := RenderLivePrompt("test", body, "/repo", skills, agents, docs, []promptTool{{Name: "read"}})
	if err != nil {
		t.Fatal(err)
	}

	stack := BuildPromptStack("test", body, rendered, skills, agents, docs)
	if stack.TotalWords != 7 { // body 4 + docs 3; skill/agent catalog entries and rendered total do not double-count.
		t.Fatalf("total words = %d, want 7; stack=%#v", stack.TotalWords, stack)
	}
	assertFragment(t, stack, prompts.FragmentSystem, "test", true)
	assertFragment(t, stack, prompts.FragmentWorkspaceInstruction, "AGENTS.md", true)
	assertFragment(t, stack, prompts.FragmentSkill, "go-testing", false)
	assertFragment(t, stack, prompts.FragmentAgent, "reviewer", false)
	assertFragment(t, stack, prompts.FragmentRenderedTotal, "test", true)
	assertPromptContains(t, rendered, "You are an agent.", "Always run tests.")
}

func assertFragment(t *testing.T, stack prompts.Stack, kind prompts.FragmentKind, name string, contributes bool) {
	t.Helper()
	for _, f := range stack.Fragments {
		if f.Kind == kind && f.Name == name {
			if f.Contributes != contributes {
				t.Fatalf("fragment %s/%s contributes=%v, want %v", kind, name, f.Contributes, contributes)
			}
			return
		}
	}
	t.Fatalf("missing fragment %s/%s in %#v", kind, name, stack.Fragments)
}
