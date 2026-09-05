package instructions_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zarldev/zarlmono/zarlcode/instructions"
)

func TestIndexApplicableReturnsNearestGuidanceFirst(t *testing.T) {
	t.Parallel()
	index := instructions.NewIndex([]instructions.NestedDoc{
		{RelPath: "pkg/AGENTS.md"},
		{RelPath: "pkg/deep/CLAUDE.md"},
		{RelPath: "other/AGENTS.md"},
		{RelPath: "AGENTS.md"},
	})

	got := index.Applicable("pkg/deep/file.go")
	want := []string{"pkg/deep/CLAUDE.md", "pkg/AGENTS.md"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("applicable guidance mismatch (-want +got):\n%s", diff)
	}
}

func TestIndexApplicableDeduplicatesAcrossPaths(t *testing.T) {
	t.Parallel()
	index := instructions.NewIndex([]instructions.NestedDoc{
		{RelPath: `pkg\AGENTS.md`},
		{RelPath: "pkg/AGENTS.md"},
	})

	got := index.Applicable("pkg/a.go", `pkg\sub\b.go`)
	want := []string{"pkg/AGENTS.md"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("applicable guidance mismatch (-want +got):\n%s", diff)
	}
}

func TestIndexApplicableRejectsOutsideAndSiblingPaths(t *testing.T) {
	t.Parallel()
	index := instructions.NewIndex([]instructions.NestedDoc{
		{RelPath: "pkg/AGENTS.md"},
		{RelPath: "sibling/AGENTS.md"},
	})

	if got := index.Applicable("other/file.go", "../pkg/file.go", "/pkg/file.go"); len(got) != 0 {
		t.Fatalf("unexpected guidance: %v", got)
	}
}
