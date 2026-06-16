package code_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestWorkspace_RejectsEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	if _, err := ws.Resolve("../etc/passwd"); err == nil {
		t.Fatal("expected escape to be rejected")
	}
	if _, err := ws.Resolve("/etc/passwd"); err == nil {
		t.Fatal("expected absolute outside-root path to be rejected")
	}
}

func TestWorkspace_ResolvesRelativeAndAbsolute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	rel, err := ws.Resolve("sub/file.txt")
	if err != nil {
		t.Fatalf("Resolve relative: %v", err)
	}
	if !strings.HasPrefix(rel, ws.Root()) {
		t.Errorf("relative resolved outside root: %s", rel)
	}

	abs, err := ws.Resolve(filepath.Join(ws.Root(), "x.txt"))
	if err != nil {
		t.Fatalf("Resolve absolute: %v", err)
	}
	if !strings.HasPrefix(abs, ws.Root()) {
		t.Errorf("absolute resolved outside root: %s", abs)
	}
}

func TestWorkspace_EmptyPathRejected(t *testing.T) {
	t.Parallel()
	ws, _ := code.NewWorkspace(t.TempDir())
	if _, err := ws.Resolve(""); err == nil {
		t.Fatal("expected empty-path rejection")
	}
}
