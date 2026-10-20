package discoveryfile_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/internal/discoveryfile"
)

func TestWalkDirCancellationAndCompatibility(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/first", "b/skip", "c/last"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var want, got []string
	visit := func(paths *[]string) fs.WalkDirFunc {
		return func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			*paths = append(*paths, path)
			if entry.Name() == "b" {
				return fs.SkipDir
			}
			return nil
		}
	}
	if err := filepath.WalkDir(root, visit(&want)); err != nil {
		t.Fatal(err)
	}
	if err := discoveryfile.WalkDir(t.Context(), root, visit(&got)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walk = %v, want %v", got, want)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("stop traversal")
	defer cancel(cause)
	visits := 0
	err := discoveryfile.WalkDir(ctx, root, func(string, fs.DirEntry, error) error {
		visits++
		if visits == 2 {
			cancel(cause)
		}
		return nil
	})
	if !errors.Is(err, cause) || visits != 2 {
		t.Fatalf("cancel walk: visits=%d, error=%v", visits, err)
	}
}
