package discoveryfile

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ReadDir checks cancellation between bounded directory reads and preserves the
// lexical ordering of os.ReadDir. It cannot interrupt an in-progress syscall.
func ReadDir(ctx context.Context, path string) ([]fs.DirEntry, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	f, err := openDirectory(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []fs.DirEntry
	for {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		batch, err := f.ReadDir(256)
		entries = append(entries, batch...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// WalkDir preserves filepath.WalkDir's lexical traversal and skip semantics,
// with cooperative cancellation during directory enumeration as well as visits.
func WalkDir(ctx context.Context, root string, visit fs.WalkDirFunc) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return visit(root, nil, err)
	}
	err = walkDir(ctx, root, fs.FileInfoToDirEntry(info), visit)
	if errors.Is(err, fs.SkipDir) || errors.Is(err, fs.SkipAll) {
		return nil
	}
	return err
}

func walkDir(ctx context.Context, path string, entry fs.DirEntry, visit fs.WalkDirFunc) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := visit(path, entry, nil); err != nil || !entry.IsDir() {
		return err
	}
	entries, err := ReadDir(ctx, path)
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err != nil {
		return visit(path, entry, err)
	}
	for _, child := range entries {
		err := walkDir(ctx, filepath.Join(path, child.Name()), child, visit)
		if errors.Is(err, fs.SkipDir) && child.IsDir() {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}
