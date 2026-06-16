package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRuntimeCatalogToolsAndPrompt(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".zarlcode", "skills", "edit.md"), `---
name: edit
description: recover from edit failures
---

Use smaller old_string matches.
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
	for _, name := range []tools.ToolName{ToolNameLoadSkill, ToolNameListSkills, ToolNameListAgents} {
		if !seen[name] {
			t.Fatalf("tool %s not registered; saw %#v", name, seen)
		}
	}

	prompt, err := RenderLivePrompt("test", `{{ range .Skills }}skill={{ .Name }} {{ end }}{{ range .Agents }}agent={{ .Name }} {{ .Model }}{{ end }}`,
		root, l.catalog.Skills(), l.catalog.Agents(), nil, []promptTool{{Name: "spawn_agent"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "skill=edit") || !strings.Contains(prompt, "agent=reviewer tiny-reviewer") {
		t.Fatalf("prompt missing catalog entries: %q", prompt)
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

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
