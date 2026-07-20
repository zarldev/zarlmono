package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	l := NewLiveRunner(nil, ws, nil, "local")
	l.catalog.Reload(root)

	if path, ok := l.catalog.Lookup("edit"); !ok || !strings.HasSuffix(path, filepath.Join("skills", "edit.md")) {
		t.Fatalf("skill lookup = (%q,%v), want edit skill path", path, ok)
	}
	if _, ok := l.catalog.Agent("reviewer"); !ok {
		t.Fatalf("agent reviewer not loaded")
	}

	src, _, err := l.source("")
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	seen := map[tools.ToolName]bool{}
	for tool := range src.Tools(t.Context()) {
		seen[tool.Definition().Name] = true
	}
	for _, name := range []tools.ToolName{ToolNameLoadSkill, ToolNameListAgents} {
		if !seen[name] {
			t.Fatalf("tool %s not registered; saw %#v", name, seen)
		}
	}

	prompt, err := RenderLivePrompt("test", LiveSystemPromptTemplate,
		root, l.catalog.Skills(), l.catalog.Agents(), nil, []promptTool{{Name: "spawn_agent"}}, "")
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
		t.Fatalf("load_skill result = %#v", res)
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
		t.Fatalf("load_skill did not refresh on miss: %#v", res)
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
