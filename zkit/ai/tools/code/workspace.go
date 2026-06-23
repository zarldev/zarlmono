// Package code holds the file-editing tools used by the coder profile.
// All path-taking tools share a Workspace value that enforces a root
// boundary — no tool in this package may touch paths outside that root.
package code

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/filesystem"
	"github.com/zarldev/zarlmono/zkit/zsync"
)

// Workspace is a filesystem boundary. All paths handed to a code tool are
// resolved against the root; anything that would resolve outside the root
// (including via symlinks or "..") is refused.
//
// Workspace values are passed by value to each tool but share their
// path-lock map AND the openat-style [os.Root] handle via pointer, so
// concurrent write/edit calls to the same path serialise correctly
// even when issued through different tool instances, and every
// writer benefits from the root-anchored open without re-opening
// the directory handle per call.
//
// TOCTOU hardening: the per-tool boundary check via [Workspace.Resolve]
// uses lexical + EvalSymlinks-of-prefix matching, which is sufficient
// to reject obviously-out-of-tree paths cheaply. The actual file
// open then goes through the [*os.Root] handle so that even if the
// parent directory is swapped for a symlink between resolve and
// open, the kernel-level traversal refuses to follow it. Earlier
// shape did `os.WriteFile(abs, ...)` against an arbitrary absolute
// path — `Resolve` told you the path was safe at check time but
// nothing stopped a concurrent local actor from escaping the root
// between check and use.
type Workspace struct {
	root      string
	osRoot    *os.Root
	pathLocks *pathLockMap
}

// pathLockMap dispenses per-path mutexes keyed by canonical absolute
// path. Used by write/edit/append to serialise writers targeting the
// same file, so a parallel tool batch from a single LLM turn doesn't
// lose updates when two tools target the same path.
type pathLockMap struct {
	m zsync.Map[string, *sync.Mutex]
}

func (p *pathLockMap) lock(absPath string) func() {
	mu, _ := p.m.LoadOrStore(absPath, &sync.Mutex{})
	mu.Lock()
	return mu.Unlock
}

// NewWorkspace returns a Workspace rooted at the given absolute path.
// The root is canonicalized via filepath.EvalSymlinks so symlink-to-outside
// checks compare apples to apples, then opened as an [os.Root] so
// subsequent file operations can use openat-style traversal that
// refuses to escape the directory regardless of symlinks underneath.
func NewWorkspace(root string) (Workspace, error) {
	if root == "" {
		return Workspace{}, errors.New("workspace: root must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace abs %q: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace eval %q: %w", abs, err)
	}
	r, err := os.OpenRoot(resolved)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace open root %q: %w", resolved, err)
	}
	return Workspace{root: resolved, osRoot: r, pathLocks: &pathLockMap{}}, nil
}

// OSRoot returns the underlying [os.Root] handle. Writers in this
// package use it for openat-style file operations; external callers
// almost never need it. May be nil for zero-value Workspaces (e.g.
// constructed in tests without NewWorkspace); callers should fall
// back to plain os operations in that case.
func (w Workspace) OSRoot() *os.Root { return w.osRoot }

// RelToRoot returns the path of abs relative to the workspace root.
// Used by writers to convert the absolute path Resolve returned into
// the root-relative form [os.Root] expects. Returns an error if abs
// is outside the root — defensive check; callers normally pass a
// path that Resolve already validated.
func (w Workspace) RelToRoot(abs string) (string, error) {
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return "", fmt.Errorf("workspace: rel %q from %q: %w", abs, w.root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("workspace: %q escapes root %q", abs, w.root)
	}
	return rel, nil
}

// WriteFileInRoot writes data to the workspace-relative path via the
// [os.Root] handle. Parent directories are created (also via the
// root handle) up to the workspace root — never beyond.
//
// This is the TOCTOU-safe replacement for the [os.WriteFile] +
// [os.MkdirAll] pair the writers used to call directly. The kernel
// refuses to follow symlinks out of the root even if a directory in
// the path is swapped for a symlink between Resolve and Write.
//
// Falls back to plain os operations for zero-value Workspaces
// (tests constructing tools without NewWorkspace); production paths
// always go through the os.Root handle.
func (w Workspace) WriteFileInRoot(abs string, data []byte, perm os.FileMode) error {
	if w.osRoot == nil {
		// Zero-value Workspace — fall back to the unsafe path so test
		// fixtures keep working. Production NewWorkspace always sets
		// osRoot.
		if err := os.MkdirAll(filepath.Dir(abs), filesystem.ModePublicDir); err != nil {
			return err
		}
		return os.WriteFile(abs, data, perm)
	}
	rel, err := w.RelToRoot(abs)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(rel); dir != "" && dir != "." {
		if err := w.osRoot.MkdirAll(dir, filesystem.ModePublicDir); err != nil {
			return fmt.Errorf("workspace mkdir %q: %w", dir, err)
		}
	}
	// Use OpenFile with O_NOFOLLOW-equivalent (os.Root refuses to
	// traverse symlinks) for the create+truncate semantics WriteFile
	// has. The standard library's os.Root.WriteFile would be ideal
	// but isn't exposed; manual open + write + close is the same
	// shape under the hood.
	f, err := w.osRoot.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("workspace open %q: %w", rel, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("workspace write %q: %w", rel, err)
	}
	return f.Close()
}

// OpenFileInRoot opens the named file relative to the workspace
// root. Same TOCTOU-safety argument as WriteFileInRoot — uses
// openat-style traversal so a symlinked parent can't escape.
func (w Workspace) OpenFileInRoot(abs string, flag int, perm os.FileMode) (*os.File, error) {
	if w.osRoot == nil {
		return os.OpenFile(abs, flag, perm)
	}
	rel, err := w.RelToRoot(abs)
	if err != nil {
		return nil, err
	}
	return w.osRoot.OpenFile(rel, flag, perm)
}

// ReadFileInRoot reads abs through the workspace root handle. This is
// the read-side companion to WriteFileInRoot: callers still use Resolve
// for friendly errors and path locks, but the actual open happens
// relative to os.Root so a concurrent symlink swap cannot escape.
func (w Workspace) ReadFileInRoot(abs string) ([]byte, error) {
	if w.osRoot == nil {
		return os.ReadFile(abs)
	}
	rel, err := w.RelToRoot(abs)
	if err != nil {
		return nil, err
	}
	return w.osRoot.ReadFile(rel)
}

// StatInRoot stats abs through the workspace root handle.
func (w Workspace) StatInRoot(abs string) (os.FileInfo, error) {
	if w.osRoot == nil {
		return os.Stat(abs)
	}
	rel, err := w.RelToRoot(abs)
	if err != nil {
		return nil, err
	}
	return w.osRoot.Stat(rel)
}

// ReadDirInRoot reads a directory through the workspace root handle.
func (w Workspace) ReadDirInRoot(abs string) ([]os.DirEntry, error) {
	if w.osRoot == nil {
		return os.ReadDir(abs)
	}
	rel, err := w.RelToRoot(abs)
	if err != nil {
		return nil, err
	}
	return fs.ReadDir(w.osRoot.FS(), rel)
}

// MkdirParentInRoot creates the parent directory of abs (if it
// doesn't already exist) via the openat-style [os.Root] handle. Use
// before [OpenFileInRoot] when the target path is a fresh write
// into a directory that may not exist yet.
func (w Workspace) MkdirParentInRoot(abs string) error {
	if w.osRoot == nil {
		return os.MkdirAll(filepath.Dir(abs), filesystem.ModePublicDir)
	}
	rel, err := w.RelToRoot(abs)
	if err != nil {
		return err
	}
	dir := filepath.Dir(rel)
	if dir == "" || dir == "." {
		return nil
	}
	return w.osRoot.MkdirAll(dir, filesystem.ModePublicDir)
}

// RemoveInRoot removes the file at the workspace-relative path via
// the [os.Root] handle. Mirrors os.Remove semantics (file-only;
// directories are out of scope for code tools).
func (w Workspace) RemoveInRoot(abs string) error {
	if w.osRoot == nil {
		return os.Remove(abs)
	}
	rel, err := w.RelToRoot(abs)
	if err != nil {
		return err
	}
	return w.osRoot.Remove(rel)
}

// Root returns the canonicalized root path.
func (w Workspace) Root() string { return w.root }

// LockPath acquires the per-path mutex for the given (already-resolved)
// absolute path and returns the unlock function. Callers must defer the
// returned unlock. Used by write/edit/append-tools to serialise writers
// targeting the same file.
func (w Workspace) LockPath(absPath string) func() {
	if w.pathLocks == nil {
		// Defensive: zero-value Workspace (e.g. tests constructing tools
		// without going through NewWorkspace). Fall back to no-op.
		return func() {}
	}
	return w.pathLocks.lock(absPath)
}

// Resolve returns the cleaned absolute path for p, where p may be relative
// (joined to root) or absolute (must be inside root). Symlinks are followed
// and the final path is re-checked against root.
func (w Workspace) Resolve(p string) (string, error) {
	return w.ResolveForRead(p, false)
}

// ResolveForRead resolves p for a read-side tool. When unrestricted is false it
// enforces the workspace root exactly like Resolve. When unrestricted is true,
// reads may escape the workspace: absolute paths are used as-is and relative
// paths are cleaned after joining to the workspace root, so ../ segments may
// walk out.
func (w Workspace) ResolveForRead(p string, unrestricted bool) (string, error) {
	if p == "" {
		return "", errors.New("workspace: path must not be empty")
	}

	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(w.root, target)
	}
	target = filepath.Clean(target)

	resolved := evalExisting(target)
	if unrestricted {
		return resolved, nil
	}
	if !w.contains(target) {
		return "", fmt.Errorf("workspace: path %q escapes root %q", p, w.root)
	}
	if !w.contains(resolved) {
		return "", fmt.Errorf("workspace: path %q resolves outside root %q", p, w.root)
	}
	return resolved, nil
}

// StatPath stats abs either through the workspace root handle (for paths still
// under root) or directly through the host filesystem (for unrestricted
// read-side paths outside the workspace).
func (w Workspace) StatPath(abs string) (os.FileInfo, error) {
	if w.contains(abs) {
		return w.StatInRoot(abs)
	}
	return os.Stat(abs)
}

// ReadFilePath reads abs through the workspace root handle when it remains
// under the workspace, falling back to direct host reads for unrestricted
// read-side paths outside the workspace.
func (w Workspace) ReadFilePath(abs string) ([]byte, error) {
	if w.contains(abs) {
		return w.ReadFileInRoot(abs)
	}
	return os.ReadFile(abs)
}

// ReadDirPath reads a directory via the workspace root handle when it remains
// under the workspace, falling back to direct host directory reads for
// unrestricted read-side paths outside the workspace.
func (w Workspace) ReadDirPath(abs string) ([]os.DirEntry, error) {
	if w.contains(abs) {
		return w.ReadDirInRoot(abs)
	}
	return os.ReadDir(abs)
}

func (w Workspace) contains(p string) bool {
	rel, err := filepath.Rel(w.root, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..")
}

// evalExisting resolves symlinks for the longest existing prefix of p
// and re-joins the missing tail.
func evalExisting(p string) string {
	parts := []string{}
	cur := p
	for {
		if realPath, err := filepath.EvalSymlinks(cur); err == nil {
			final := filepath.Join(append([]string{realPath}, reverse(parts)...)...)
			return filepath.Clean(final)
		}
		dir, base := filepath.Split(cur)
		if dir == "" || dir == "/" {
			return p
		}
		parts = append(parts, base)
		cur = filepath.Clean(dir)
	}
}

func reverse(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}
