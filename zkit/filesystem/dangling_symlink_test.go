package filesystem_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/filesystem"
)

func TestOSFileSystemRejectsDanglingSymlinks(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"write", "open", "mkdir"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			base, outside := t.TempDir(), t.TempDir()
			target := filepath.Join(outside, "missing")
			if err := os.Symlink(target, filepath.Join(base, "link")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			fs := filesystem.NewOSFileSystem(base)
			var err error
			switch operation {
			case "write":
				err = fs.WriteFile("link", []byte("escape"), 0o600)
			case "open":
				f, openErr := fs.OpenFile("link", os.O_CREATE|os.O_WRONLY, 0o600)
				err = openErr
				if f != nil {
					_ = f.Close()
				}
			case "mkdir":
				err = fs.MkdirAll("link/child", 0o700)
			}
			if !errors.Is(err, filesystem.ErrEscapesRoot) {
				t.Fatalf("%s: got %v, want ErrEscapesRoot", operation, err)
			}
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("outside target exists or cannot be checked: %v", err)
			}
		})
	}
}
