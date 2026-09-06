package repohealth_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/repohealth"
)

func TestCheckAcceptsRepositoryLocalGuidance(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "Use [go-style](.zarlcode/skills/go-style/SKILL.md).\n")
	writeFile(t, filepath.Join(root, ".zarlcode", "skills", "go-style", "SKILL.md"), `---
name: go-style
description: Go policy.
---

See [package guidance](../../../pkg/AGENTS.md).
`)
	writeFile(t, filepath.Join(root, ".zarlcode", "agents", "reviewer.md"), `---
name: reviewer
description: Review repository changes.
mode: verify
---

Review without editing.
`)
	writeFile(t, filepath.Join(root, "pkg", "AGENTS.md"), "Follow the [root guidance](../AGENTS.md).\n")

	if err := repohealth.Check(root); err != nil {
		t.Fatalf("Check valid guidance: %v", err)
	}
}

func TestCheckRejectsSkillDirectoryMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "Use [wrong](.zarlcode/skills/right/SKILL.md).\n")
	writeFile(t, filepath.Join(root, ".zarlcode", "skills", "right", "SKILL.md"), `---
name: wrong
description: Mismatched skill.
---

Instructions.
`)

	err := repohealth.Check(root)
	if err == nil || !strings.Contains(err.Error(), `frontmatter name "wrong" does not match directory "right"`) {
		t.Fatalf("Check error = %v; want skill directory mismatch", err)
	}
}

func TestCheckRejectsBrokenGuidanceLink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "See [missing](pkg/AGENTS.md).\n")
	writeFile(t, filepath.Join(root, ".zarlcode", "skills", "go-style", "SKILL.md"), `---
name: go-style
description: Go policy.
---

Instructions.
`)

	err := repohealth.Check(root)
	if err == nil || !strings.Contains(err.Error(), `local link "pkg/AGENTS.md" does not resolve`) {
		t.Fatalf("Check error = %v; want broken local link", err)
	}
}

func TestCheckIgnoresPullRequestWorktrees(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "Use [go-style](.zarlcode/skills/go-style/SKILL.md).\n")
	writeFile(t, filepath.Join(root, ".zarlcode", "skills", "go-style", "SKILL.md"), `---
name: go-style
description: Go policy.
---

Instructions.
`)
	writeFile(t, filepath.Join(root, ".zarlcode", "pr-worktrees", "old", "AGENTS.md"), "See [missing](missing.md).\n")

	if err := repohealth.Check(root); err != nil {
		t.Fatalf("Check with ignored worktree: %v", err)
	}
}

func TestCheckRejectsInvalidAgentMode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "Use [go-style](.zarlcode/skills/go-style/SKILL.md).\n")
	writeFile(t, filepath.Join(root, ".zarlcode", "skills", "go-style", "SKILL.md"), `---
name: go-style
description: Go policy.
---

Instructions.
`)
	writeFile(t, filepath.Join(root, ".zarlcode", "agents", "reviewer.md"), `---
name: reviewer
description: Review repository changes.
mode: mutate
---

Review.
`)

	err := repohealth.Check(root)
	if err == nil || !strings.Contains(err.Error(), `unsupported mode "mutate"`) {
		t.Fatalf("Check error = %v; want invalid agent mode", err)
	}
}

func TestRepositoryGuidance(t *testing.T) {
	if err := repohealth.Check(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
