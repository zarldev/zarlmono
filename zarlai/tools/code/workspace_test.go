package code_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/tools/code"
)

func TestWorkspace_Resolve(t *testing.T) {
	root := t.TempDir()
	// Create a subdir, a file, and a symlink-to-outside for the table.
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "inside-link")); err != nil {
		t.Fatal(err)
	}

	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"relative inside", "sub/f.txt", false},
		{"absolute inside", filepath.Join(root, "sub", "f.txt"), false},
		{"absolute outside", outside, true},
		{"dotdot escape", "../etc/passwd", true},
		{"symlink to outside", "escape/anything", true},
		{"symlink to inside", "inside-link/f.txt", false},
		{"empty path", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ws.Resolve(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			rel, relErr := filepath.Rel(root, got)
			if relErr != nil || rel == ".." || filepath.IsAbs(rel) || rel[:2] == ".." {
				// rel starting with ".." would mean escape
				t.Fatalf("resolved path %q escapes root %q", got, root)
			}
		})
	}
}

func TestWorkspace_Root(t *testing.T) {
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Root() != root {
		t.Fatalf("Root() = %q, want %q", ws.Root(), root)
	}
}
