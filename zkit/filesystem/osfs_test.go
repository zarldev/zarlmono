package filesystem_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// TestOSFileSystem_RejectsAbsolutePath guards the lexical-check half
// of resolveInsideRoot: a caller passing a fully-qualified path
// (e.g. "/etc/passwd") must not escape the configured base.
func TestOSFileSystem_RejectsAbsolutePath(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fs := filesystem.NewOSFileSystem(base)

	_, err := fs.ReadFile("/etc/passwd")
	if !errors.Is(err, filesystem.ErrEscapesRoot) {
		t.Errorf("ReadFile(/etc/passwd) err = %v, want ErrEscapesRoot", err)
	}

	if err := fs.WriteFile("/tmp/escape", []byte("nope"), 0o644); !errors.Is(err, filesystem.ErrEscapesRoot) {
		t.Errorf("WriteFile(/tmp/escape) err = %v, want ErrEscapesRoot", err)
	}
}

// TestOSFileSystem_RejectsDotDotTraversal guards lexical rejection
// of "../" prefixes. After filepath.Clean, the path "../escape"
// remains "../escape" which is unambiguously out-of-tree.
func TestOSFileSystem_RejectsDotDotTraversal(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fs := filesystem.NewOSFileSystem(base)

	cases := []string{
		"../escape",
		"../../escape",
		"sub/../../escape", // clean → ../escape
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			_, err := fs.ReadFile(p)
			if !errors.Is(err, filesystem.ErrEscapesRoot) {
				t.Errorf("ReadFile(%q) err = %v, want ErrEscapesRoot", p, err)
			}
		})
	}
}

// TestOSFileSystem_RejectsSymlinkEscape exercises the EvalSymlinks
// half of resolveInsideRoot: a symlink inside the workspace pointing
// at a parent path must be refused, even though the lexical form
// (e.g. "link/x") looks benign.
func TestOSFileSystem_RejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	outside := t.TempDir() // separate dir we'll symlink to

	// Write a victim file outside the workspace.
	victim := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(victim, []byte("secret data"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	// link inside base → outside
	link := filepath.Join(base, "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	fs := filesystem.NewOSFileSystem(base)
	_, err := fs.ReadFile("escape-link/secret.txt")
	if !errors.Is(err, filesystem.ErrEscapesRoot) {
		t.Errorf("ReadFile through escape symlink err = %v, want ErrEscapesRoot", err)
	}
}

// TestOSFileSystem_HappyPathAllowsRelativePaths is the negative
// control — a perfectly normal relative write/read must still
// succeed under the hardened resolver.
func TestOSFileSystem_HappyPathAllowsRelativePaths(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fs := filesystem.NewOSFileSystem(base)

	if err := fs.WriteFile("nested/file.txt", []byte("ok"), 0o644); err == nil {
		// fs.WriteFile doesn't auto-MkdirAll; that's the expected
		// behaviour for the raw filesystem. The test just checks
		// the resolver didn't preemptively reject the relative path.
	} else if !strings.Contains(err.Error(), "no such file or directory") &&
		!errors.Is(err, os.ErrNotExist) {
		t.Errorf("WriteFile relative path err = %v, expected at most a missing-dir error", err)
	}

	if err := fs.MkdirAll("ok", 0o755); err != nil {
		t.Errorf("MkdirAll relative: %v", err)
	}
	if err := fs.WriteFile("ok/file.txt", []byte("data"), 0o644); err != nil {
		t.Errorf("WriteFile inside mkdir: %v", err)
	}
	data, err := fs.ReadFile("ok/file.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "data" {
		t.Errorf("ReadFile = %q, want %q", string(data), "data")
	}
}
