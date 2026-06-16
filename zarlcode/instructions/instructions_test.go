package instructions_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zarldev/zarlmono/zarlcode/instructions"
)

func TestDiscoverFindsRootAndNestedInstructionsInStableOrder(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "root claude")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root agents")
	mustWrite(t, filepath.Join(root, "pkg", "CLAUDE.md"), "pkg claude")
	mustWrite(t, filepath.Join(root, "pkg", "AGENTS.md"), "pkg agents")
	mustWrite(t, filepath.Join(root, "pkg", "deep", "AGENTS.md"), "deep agents")

	docs, errs := instructions.Discover(root, instructions.DefaultMaxBytes)
	if len(errs) > 0 {
		t.Fatalf("discover errors: %v", errs)
	}

	var got []string
	for _, doc := range docs {
		got = append(got, doc.RelPath)
	}
	want := []string{
		"AGENTS.md",
		"CLAUDE.md",
		"pkg/AGENTS.md",
		"pkg/CLAUDE.md",
		"pkg/deep/AGENTS.md",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("instruction order mismatch (-want +got):\n%s", diff)
	}
}

func TestDiscoverIgnoresNoisyDirectories(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root")
	ignored := []string{
		".git",
		filepath.Join(".zarlcode", "sessions"),
		"node_modules",
		"vendor",
		"dist",
		"build",
		"coverage",
	}
	for _, dir := range ignored {
		mustWrite(t, filepath.Join(root, dir, "AGENTS.md"), "ignored")
		mustWrite(t, filepath.Join(root, dir, "CLAUDE.md"), "ignored")
	}

	docs, errs := instructions.Discover(root, instructions.DefaultMaxBytes)
	if len(errs) > 0 {
		t.Fatalf("discover errors: %v", errs)
	}
	var got []string
	for _, doc := range docs {
		got = append(got, doc.RelPath)
	}
	want := []string{"AGENTS.md"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("ignored directory mismatch (-want +got):\n%s", diff)
	}
}

func TestDiscoverByteCapTruncatesDocument(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "0123456789abcdef")
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "should not fit")

	docs, errs := instructions.Discover(root, 8)
	if len(errs) > 0 {
		t.Fatalf("discover errors: %v", errs)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	if !docs[0].Truncated {
		t.Fatalf("doc was not marked truncated: %#v", docs[0])
	}
	if !strings.Contains(docs[0].Content, "01234567") || !strings.Contains(docs[0].Content, "truncated") {
		t.Fatalf("truncated content missing prefix/notice: %q", docs[0].Content)
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
