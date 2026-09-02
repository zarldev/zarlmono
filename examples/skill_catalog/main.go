// Binary skill_catalog demonstrates on-disk skill discovery followed by loading
// the discovered guides into the versioned in-memory skill store.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zkit/skills"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "skill catalog:", err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	root, err := os.MkdirTemp("", "skill-catalog-example-")
	if err != nil {
		return fmt.Errorf("create temp root: %w", err)
	}
	defer os.RemoveAll(root)

	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", filepath.Join(root, "home")); err != nil {
		return fmt.Errorf("set temporary HOME: %w", err)
	}
	defer os.Setenv("HOME", oldHome)

	workspace := filepath.Join(root, "workspace")
	skillPath := filepath.Join(workspace, ".zarlcode", "skills", "release-notes", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		return fmt.Errorf("create skill package: %w", err)
	}
	const markdown = `---
name: release-notes
description: Draft concise release notes from a local change list.
---
# Release notes

Group changes by user-visible impact.
`
	if err := os.WriteFile(skillPath, []byte(markdown), 0o600); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}

	discovered, loadErrs := catalog.LoadSkills(workspace)
	if len(loadErrs) != 0 {
		return fmt.Errorf("discover skills: %v", loadErrs)
	}

	store := skills.NewMemorySkillStore()
	loaded := make([]skills.Skill, 0, len(discovered))
	for _, skill := range discovered {
		loaded = append(loaded, skills.Skill{
			ID:          skill.Name,
			Name:        skill.Name,
			Description: skill.Description,
			Markdown:    skill.Body,
		})
	}
	store.Load(loaded)

	current := store.EnabledSkills()
	fmt.Fprintf(out, "discovered=%d loaded=%d version=%d\n", len(discovered), len(current), store.Version())
	fmt.Fprintf(out, "name=%s description=%q\n", current[0].Name, current[0].Description)
	fmt.Fprintf(out, "body=%q\n", current[0].Markdown)
	return nil
}
