package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	_ ReadWriteFileFS = (*OSFileSystem)(nil)
)

// ErrEscapesRoot is returned by [OSFileSystem] operations when the
// supplied path resolves outside the configured base directory.
// Surfaced as a sentinel so callers can switch on it.
var ErrEscapesRoot = errors.New("filesystem: path escapes base directory")

// OSFileSystem implements ReadWriteFileFS using the standard os
// package, confined to a base directory.
//
// Earlier shape concatenated `filepath.Join(baseDir, userPath)`
// without any escape check — `..` traversal, absolute paths, and
// symlink follow-out-of-tree all worked. Direct consumers
// (especially anything taking user / agent input) could escape the
// configured root unintentionally. The current shape resolves every
// path through [resolveInsideRoot] which rejects:
//
//   - absolute user paths (UNIX-style and Windows drive-style)
//   - cleaned paths that begin with ".." or escape via "../"
//   - paths whose post-realpath answer lives outside the root
//
// The symlink check is best-effort against TOCTOU — a concurrent
// local actor could swap a checked component for a symlink between
// resolve and use. Callers that need stronger guarantees should
// hold the parent directory open and use openat(2)-style traversal;
// this package's [OSFileSystem] is the cooperative-trust default,
// not the hardened one.
type OSFileSystem struct {
	baseDir string
}

// NewOSFileSystem creates a new OS filesystem with the specified base directory.
// The base directory will be used as the root for all file operations.
func NewOSFileSystem(baseDir string) *OSFileSystem {
	return &OSFileSystem{baseDir: baseDir}
}

// resolveInsideRoot joins userPath against the base directory and
// rejects results that escape the root.
//
// The check is two-layered:
//
//  1. Cheap lexical check: reject absolute paths (Unix /foo,
//     Windows C:\foo) and paths that resolve to "/" + "../" after
//     filepath.Clean. These are the easy cases and they're caught
//     without touching the filesystem.
//
//  2. EvalSymlinks-then-Rel check: resolve any symlinks in the
//     existing portion of the path and verify the result still
//     lives under the resolved base directory. This catches symlink
//     escapes (a/b symlinked to /etc/passwd) where the lexical
//     form looks benign.
//
// When the target file does not yet exist (Write / MkdirAll on a
// fresh path), the symlink check falls back to resolving the
// longest existing prefix — partial enforcement is better than
// none, and the missing component can't itself be a symlink yet.
func (osfs *OSFileSystem) resolveInsideRoot(userPath string) (string, error) {
	if filepath.IsAbs(userPath) {
		return "", fmt.Errorf("%w: absolute path %q", ErrEscapesRoot, userPath)
	}
	cleaned := filepath.Clean(userPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: traversal %q", ErrEscapesRoot, userPath)
	}
	joined := filepath.Join(osfs.baseDir, cleaned)

	// Symlink-aware boundary check. Resolve base + as much of the
	// target as currently exists; anything created later can't be a
	// pre-planted symlink (we'd have to be tricked at write time,
	// which is the TOCTOU caveat above).
	realBase, err := filepath.EvalSymlinks(osfs.baseDir)
	if err != nil {
		// Base doesn't resolve (missing, permission denied) — fail
		// closed rather than silently fall through to an
		// unconstrained join.
		return "", fmt.Errorf("%w: base %q: %w", ErrEscapesRoot, osfs.baseDir, err)
	}
	realTarget, err := evalLongestExisting(joined)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %q: %w", ErrEscapesRoot, joined, err)
	}
	rel, err := filepath.Rel(realBase, realTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q resolves outside %q", ErrEscapesRoot, userPath, osfs.baseDir)
	}
	return joined, nil
}

// evalLongestExisting EvalSymlinks the longest prefix of p that
// actually exists, then appends the remaining (not-yet-created)
// suffix. Lets us bound the resolution of write paths whose final
// component doesn't exist yet, without giving up on intermediate
// symlink detection.
func evalLongestExisting(p string) (string, error) {
	p = filepath.Clean(p)
	if realPath, err := filepath.EvalSymlinks(p); err == nil {
		return realPath, nil
	}
	// Walk up component by component until we find one that resolves;
	// everything below is "new" relative to the filesystem.
	parent := filepath.Dir(p)
	if parent == p {
		// Reached root without finding any existing ancestor — that
		// shouldn't happen on a configured baseDir, but treat it as a
		// failure rather than returning an unchecked path.
		return "", fmt.Errorf("no existing ancestor of %q", p)
	}
	realParent, err := evalLongestExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(realParent, filepath.Base(p)), nil
}

// ReadFile reads a file from the OS filesystem.
func (osfs *OSFileSystem) ReadFile(filename string) ([]byte, error) {
	abs, err := osfs.resolveInsideRoot(filename)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// WriteFile writes data to a file in the OS filesystem.
func (osfs *OSFileSystem) WriteFile(filename string, data []byte, perm fs.FileMode) error {
	abs, err := osfs.resolveInsideRoot(filename)
	if err != nil {
		return err
	}
	return os.WriteFile(abs, data, perm)
}

// Remove removes a file from the OS filesystem.
func (osfs *OSFileSystem) Remove(filename string) error {
	abs, err := osfs.resolveInsideRoot(filename)
	if err != nil {
		return err
	}
	return os.Remove(abs)
}

// MkdirAll creates a directory and all necessary parents.
func (osfs *OSFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	abs, err := osfs.resolveInsideRoot(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, perm)
}

// OpenFile opens the named file with specified flag and perm.
func (osfs *OSFileSystem) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	abs, err := osfs.resolveInsideRoot(name)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(abs, flag, perm)
}

// WalkDir walks the directory tree in the OS filesystem.
func (osfs *OSFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	abs, err := osfs.resolveInsideRoot(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(abs, fn)
}

// BaseDir returns the base directory for this filesystem.
func (osfs *OSFileSystem) BaseDir() string {
	return osfs.baseDir
}
