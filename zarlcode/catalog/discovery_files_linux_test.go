//go:build linux

package catalog_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
)

func TestCatalogSpecialFilesDoNotBlock(t *testing.T) {
	if root := os.Getenv("ZARLCODE_CATALOG_FIFO_ROOT"); root != "" {
		assertDefinitionErrors(t, root)
		return
	}
	root := t.TempDir()
	for _, rel := range []string{"agents/bad.md", "skills/bad/SKILL.md", "hooks/bad.md"} {
		path := filepath.Join(root, ".zarlcode", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCatalogSpecialFilesDoNotBlock$")
	cmd.Env = append(os.Environ(), "ZARLCODE_CATALOG_FIFO_ROOT="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("special-file catalogue: %v (context: %v)\n%s", err, ctx.Err(), output)
	}
}

func TestCatalogRejectsOversizedDefinitions(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"agents/bad.md", "skills/bad/SKILL.md", "hooks/bad.md"} {
		path := filepath.Join(root, ".zarlcode", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: bad\ndescription: oversized\nevent: pre_tool\n---\n" + strings.Repeat("x", catalog.MaxDefinitionBytes)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	assertDefinitionErrors(t, root)
}

func TestCatalogPreservesRegularFileSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "definition.md")
	body := "---\nname: shared\ndescription: shared definition\nevent: pre_tool\n---\necho safe\n"
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"agents/shared.md", "skills/shared/SKILL.md", "hooks/shared.md"} {
		path := filepath.Join(root, ".zarlcode", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	agents, agentErrs := catalog.LoadAgents(root)
	skills, skillErrs := catalog.LoadSkills(root)
	hooks, hookErrs := catalog.LoadHooks(root)
	if len(agents) != 1 || len(skills) != 1 || len(hooks) != 1 {
		t.Fatalf("regular symlink definitions: agents=%d skills=%d hooks=%d", len(agents), len(skills), len(hooks))
	}
	if agents[0].Body != "echo safe" || skills[0].Body != "echo safe" || hooks[0].Command != "echo safe" {
		t.Fatal("symlinked definition content changed")
	}
	for _, errs := range [][]error{agentErrs, skillErrs, hookErrs} {
		if len(errs) != 0 {
			t.Fatalf("regular symlink errors: %v", errs)
		}
	}
}

func assertDefinitionErrors(t *testing.T, root string) {
	t.Helper()
	agents, agentErrs := catalog.LoadAgents(root)
	skills, skillErrs := catalog.LoadSkills(root)
	hooks, hookErrs := catalog.LoadHooks(root)
	if len(agents) != 0 || len(skills) != 0 || len(hooks) != 0 {
		t.Fatalf("invalid definitions were accepted: agents=%d skills=%d hooks=%d", len(agents), len(skills), len(hooks))
	}
	for _, errs := range [][]error{agentErrs, skillErrs, hookErrs} {
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "bad") {
			t.Fatalf("definition errors = %v, want one path-qualified error", errs)
		}
	}
}
