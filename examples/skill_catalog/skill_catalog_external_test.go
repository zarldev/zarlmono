package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSkillCatalogCLI(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run skill_catalog: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"discovered=1 loaded=1 version=1",
		"name=release-notes",
		`description="Draft concise release notes from a local change list."`,
		`body="# Release notes\n\nGroup changes by user-visible impact."`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
