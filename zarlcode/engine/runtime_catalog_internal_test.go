package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRuntimeCatalogToolsDoNotInlineIntoPrompt(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".zarlcode", "skills", "edit.md"), `---
name: edit
description: recover from edit failures
---

Use narrower anchored edits.
`)
	mustWrite(t, filepath.Join(root, ".zarlcode", "agents", "reviewer.md"), `---
name: reviewer
description: review changes
model: tiny-reviewer
---

You review code changes.
`)

	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	l := NewLiveRunner(nil, ws, "local")
	l.catalog.Reload(root)

	if path, ok := l.catalog.Lookup("edit"); !ok || !strings.HasSuffix(path, filepath.Join("skills", "edit.md")) {
		t.Fatalf("skill lookup = (%q,%v), want edit skill path", path, ok)
	}
	if _, ok := l.catalog.Agent("reviewer"); !ok {
		t.Fatalf("agent reviewer not loaded")
	}

	src, _, err := l.source(t.Context(), "")
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	seen := map[tools.ToolName]bool{}
	for tool := range src.Tools(t.Context()) {
		seen[tool.Definition().Name] = true
	}
	for _, name := range []tools.ToolName{ToolNameCreateSkill, ToolNameLoadSkill, programtools.ToolName} {
		if !seen[name] {
			t.Fatalf("tool %s not registered; saw %#v", name, seen)
		}
	}

	prompt, err := RenderLivePrompt("test", LiveSystemPromptTemplate,
		root, l.catalog.Skills(), l.catalog.Agents(), nil, []promptTool{{Name: "agent_spawn"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"skill=edit", "recover from edit failures", "agent=reviewer", "tiny-reviewer"} {
		if strings.Contains(prompt, leak) {
			t.Fatalf("prompt should not inline catalog entry %q:\n%s", leak, prompt)
		}
	}
}
func TestLoadSkillTool(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".zarlcode", "skills", "go.md"), `---
name: go
description: go workflow
---

Run go test ./...
`)
	cat := newRuntimeCatalog(root)
	res, err := NewLoadSkillTool(cat).Execute(t.Context(), tools.ToolCall{
		ID:        "c1",
		ToolName:  ToolNameLoadSkill,
		Arguments: tools.ToolParameters{"name": "go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Success || !strings.Contains(res.Data.(string), "go test") {
		t.Fatalf("skill_load result = %#v", res)
	}
}

func TestCatalogListToolsRefreshAtListTime(t *testing.T) {
	root := t.TempDir()
	cat := newRuntimeCatalog(root)

	mustWrite(t, filepath.Join(root, ".zarlcode", "agents", "fresh.md"), `---
name: fresh
description: newly added agent
---

Review fresh changes.
`)
	res, err := NewListAgentsTool(cat).Execute(t.Context(), tools.ToolCall{ID: "agents", ToolName: ToolNameListAgents})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Success || !strings.Contains(res.Data.(string), "fresh") {
		t.Fatalf("list_agents did not refresh catalogue: %#v", res)
	}

	mustWrite(t, filepath.Join(root, ".zarlcode", "skills", "fresh-skill.md"), `---
name: fresh-skill
description: newly added skill
---

Use fresh skill.
`)
	res, err = NewListSkillsTool(cat).Execute(t.Context(), tools.ToolCall{ID: "skills", ToolName: ToolNameListSkills})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Success || !strings.Contains(res.Data.(string), "fresh-skill") {
		t.Fatalf("list_skills did not refresh catalogue: %#v", res)
	}
}

func TestLoadSkillToolRefreshesOnceOnMiss(t *testing.T) {
	root := t.TempDir()
	cat := newRuntimeCatalog(root)
	mustWrite(t, filepath.Join(root, ".zarlcode", "skills", "late.md"), `---
name: late
description: late skill
---

Loaded after refresh.
`)

	res, err := NewLoadSkillTool(cat).Execute(t.Context(), tools.ToolCall{
		ID:        "late",
		ToolName:  ToolNameLoadSkill,
		Arguments: tools.ToolParameters{"name": "late"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Success || !strings.Contains(res.Data.(string), "Loaded after refresh") {
		t.Fatalf("skill_load did not refresh on miss: %#v", res)
	}
}

func TestCreateSkillToolWritesPortablePackageAndReloadsCatalog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cat := newRuntimeCatalog(t.TempDir())
	tool := NewCreateSkillTool(cat)
	if !tool.Definition().Mutates {
		t.Fatal("skill_create must declare mutation for explore/verify gating")
	}
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:       "create",
		ToolName: ToolNameCreateSkill,
		Arguments: tools.ToolParameters{
			"name":         "self-extension",
			"description":  "Teach zarlcode how to extend itself when adding reusable workflows.",
			"instructions": "Use the canonical creation tools and verify discovery.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Success {
		t.Fatalf("skill_create result = %#v", res)
	}
	skill, ok := cat.Skill("self-extension")
	if !ok {
		t.Fatal("created skill was not reloaded into runtime catalog")
	}
	if !strings.HasSuffix(skill.Source, filepath.Join("self-extension", "SKILL.md")) {
		t.Fatalf("skill source = %q, want portable package", skill.Source)
	}
	data, err := os.ReadFile(skill.Source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: self-extension", "description: \"Teach zarlcode", "canonical creation tools"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("SKILL.md missing %q:\n%s", want, data)
		}
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
