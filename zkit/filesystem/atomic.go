package filesystem

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces filename with a complete copy of data using a
// temporary file in the same directory. Errors before rename leave the old
// file intact. The replacement uses perm and replaces, rather than follows,
// an existing symlink. Path checks have the same trust boundary as WriteFile.
func (osfs *OSFileSystem) WriteFileAtomic(filename string, data []byte, perm fs.FileMode) error {
	path, err := osfs.resolveInsideRoot(filename)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if err != nil {
		return fmt.Errorf("create replacement: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := writeReplacement(tmp, data, perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close replacement: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}

func writeReplacement(file *os.File, data []byte, perm fs.FileMode) error {
	if err := file.Chmod(perm); err != nil {
		return fmt.Errorf("set replacement mode: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write replacement: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync replacement: %w", err)
	}
	return nil
}

// WriteFileAtomic stores a complete, detached copy of data in one map update.
func (mfs *MemFS) WriteFileAtomic(filename string, data []byte, perm fs.FileMode) error {
	return mfs.WriteFile(filename, data, perm)
}
