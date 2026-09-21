package code_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestWorkspaceSharedCloseAndDotDotName(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	path, err := ws.Resolve(filepath.Join("..cache", "entry"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteFileInRoot(path, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	workspaceCopy := ws
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := workspaceCopy.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, err := ws.ReadFileInRoot(path); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
	if _, err := ws.Resolve("..cache"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("resolve after close: %v", err)
	}
	if _, err := ws.ReadFilePath(filepath.Join(t.TempDir(), "outside")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("host read after close: %v", err)
	}
}
