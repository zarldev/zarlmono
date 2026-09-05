package filesystem_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/filesystem"
)

func TestAtomicWriteReplacementAndCleanup(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fs := filesystem.NewOSFileSystem(base)
	if err := fs.WriteFile("entry", []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFileAtomic("entry", []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile("entry")
	if err != nil || string(data) != "new" {
		t.Fatalf("replacement = %q, %v", data, err)
	}
	if err := fs.MkdirAll("directory", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFileAtomic("directory", []byte("cannot replace a directory"), 0o600); err == nil {
		t.Fatal("expected rename error")
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
	info, err := os.Stat(filepath.Join(base, "directory"))
	if err != nil || !info.IsDir() {
		t.Fatalf("failed rename changed target: %v", err)
	}
}
