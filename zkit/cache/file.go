package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/filesystem"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrPersistenceRequired is returned by [NewPersistentFileCache] when
// the underlying OS filesystem can't be opened (MkdirTemp / MkdirAll
// failed). Callers that require durability across restarts use this
// constructor to fail loudly instead of silently falling back to an
// in-memory filesystem.
var ErrPersistenceRequired = errors.New("cache: persistence required but OS filesystem setup failed")

var (
	_ Reader[string, any]     = (*FileCache[string, any])(nil)
	_ Writer[string, any]     = (*FileCache[string, any])(nil)
	_ ReadWriter[string, any] = (*FileCache[string, any])(nil)
	_ Cache[string, any]      = (*FileCache[string, any])(nil)
)

// FileSystem defines the interface that FileCache needs from filesystem implementations.
// This follows the principle that consumers should define interfaces they depend on.
type FileSystem interface {
	// ReadFile reads the file named by filename and returns the contents.
	ReadFile(filename string) ([]byte, error)

	// WriteFile writes data to a file named by filename.
	WriteFile(filename string, data []byte, perm fs.FileMode) error

	// Remove removes the named file.
	Remove(filename string) error

	// WalkDir walks the file tree rooted at root, calling fn for each file or
	// directory in the tree, including root.
	WalkDir(root string, fn fs.WalkDirFunc) error
}

// FileCache is a thread-safe cache implementation using the file system as storage.
// It provides persistent caching capabilities across application restarts when using
// OS filesystem, or in-memory caching for testing when using MemFS (default).
type FileCache[K comparable, V any] struct {
	mu sync.RWMutex
	fs FileSystem
}

// WithFileSystem sets the filesystem implementation for the cache.
// If not provided, an in-memory filesystem (MemFS) is used by default.
func WithFileSystem[K comparable, V any](fs FileSystem) options.Option[FileCache[K, V]] {
	return func(fc *FileCache[K, V]) { fc.fs = fs }
}

// WithOSFileSystem configures the cache to use the OS filesystem at
// the given base directory. An empty path means "make a temp dir".
// On any setup error the fallback is MemFS — the cache still works,
// data just isn't persisted across restarts.
func WithOSFileSystem[K comparable, V any](baseDir string) options.Option[FileCache[K, V]] {
	return func(fc *FileCache[K, V]) { fc.fs = osBackedFS(baseDir) }
}

// NewFileCache creates a new file-based cache. Defaults to a
// per-process temp directory; pass WithFileSystem / WithOSFileSystem
// to override. Never returns an error — when persistence setup fails
// the cache silently downgrades to in-memory storage. Use
// [NewPersistentFileCache] if durability is required.
func NewFileCache[K comparable, V any](opts ...options.Option[FileCache[K, V]]) *FileCache[K, V] {
	fc := &FileCache[K, V]{fs: osBackedFS("")}
	for _, opt := range opts {
		opt(fc)
	}
	return fc
}

// NewPersistentFileCache constructs a FileCache that REQUIRES a real
// OS-backed filesystem. Returns [ErrPersistenceRequired] (wrapping
// the underlying setup error) if the temp directory or baseDir can't
// be created. Callers whose correctness depends on cross-restart
// durability use this constructor instead of [NewFileCache] so the
// downgrade-to-memory failure mode becomes a startup error rather
// than a silent data-loss bug.
func NewPersistentFileCache[K comparable, V any](
	baseDir string,
	opts ...options.Option[FileCache[K, V]],
) (*FileCache[K, V], error) {
	fsImpl, err := osBackedFSStrict(baseDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPersistenceRequired, err)
	}
	fc := &FileCache[K, V]{fs: fsImpl}
	for _, opt := range opts {
		opt(fc)
	}
	if !fc.IsPersistent() {
		// An option overrode fs with a MemFS — caller asked for
		// persistence and then took it away. Honour the contract.
		return nil, fmt.Errorf("%w: an option swapped in a non-persistent filesystem", ErrPersistenceRequired)
	}
	return fc, nil
}

// osBackedFSStrict is [osBackedFS] without the silent fallback. The
// caller (currently [NewPersistentFileCache]) is responsible for
// translating any error into a higher-level "persistence required"
// failure.
func osBackedFSStrict(baseDir string) (FileSystem, error) {
	if baseDir == "" {
		tmpDir, err := os.MkdirTemp("", "filecache-*")
		if err != nil {
			return nil, fmt.Errorf("MkdirTemp: %w", err)
		}
		baseDir = tmpDir
	}
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return nil, fmt.Errorf("MkdirAll %q: %w", baseDir, err)
	}
	return filesystem.NewOSFileSystem(baseDir), nil
}

// osBackedFS returns an OS-rooted filesystem at baseDir, creating the
// directory tree if needed. Any setup failure (TempDir, MkdirAll)
// falls back to MemFS so the constructor never panics or returns an
// error — at the cost of silently losing persistence.
//
// The fallback now emits a slog.Warn so operators can see the
// degradation in logs rather than discovering "the cache reset
// across restarts" empirically. Callers that need to detect the
// downgrade in code consult [FileCache.IsPersistent] after
// construction.
func osBackedFS(baseDir string) FileSystem {
	if baseDir == "" {
		tmpDir, err := os.MkdirTemp("", "filecache-*")
		if err != nil {
			slog.Warn("filecache: MkdirTemp failed, falling back to in-memory storage",
				"err", err, "persistent", false)
			return filesystem.NewMemFS()
		}
		baseDir = tmpDir
	}
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		slog.Warn("filecache: MkdirAll failed, falling back to in-memory storage",
			"baseDir", baseDir, "err", err, "persistent", false)
		return filesystem.NewMemFS()
	}
	return filesystem.NewOSFileSystem(baseDir)
}

// IsPersistent reports whether the cache is backed by a real
// filesystem (data survives process restart) or a MemFS (data is
// lost when the process exits). Callers that built the cache via
// [WithOSFileSystem] but need to verify the OS backend actually
// took — vs the silent fallback to MemFS on a permission / disk
// error — read this in their healthcheck.
func (c *FileCache[K, V]) IsPersistent() bool {
	_, ok := c.fs.(*filesystem.MemFS)
	return !ok
}

// Set stores a key-value pair as a file on disk.
// If the key already exists, its value is updated.
func (c *FileCache[K, V]) Set(ctx context.Context, key K, value V) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	filename := c.makeFilename(key)
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.fs.WriteFile(filename, data, 0644)
}

// Get retrieves the value associated with the given key from disk.
// Returns ErrNotFound if the key does not exist.
func (c *FileCache[K, V]) Get(ctx context.Context, key K) (V, error) {
	select {
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	default:
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	filename := c.makeFilename(key)
	data, err := c.fs.ReadFile(filename)
	if err != nil {
		var zero V
		if os.IsNotExist(err) {
			return zero, ErrNotFound
		}
		return zero, err
	}

	var value V
	if err := json.Unmarshal(data, &value); err != nil {
		var zero V
		return zero, err
	}

	return value, nil
}

// Delete removes a key-value pair from disk.
// Returns true if the key existed and was deleted, false otherwise.
func (c *FileCache[K, V]) Delete(ctx context.Context, key K) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	filename := c.makeFilename(key)
	err := c.fs.Remove(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// Clear removes all cache files from the base directory.
func (c *FileCache[K, V]) Clear(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if memfs, ok := c.fs.(*filesystem.MemFS); ok {
		// use memfs optimized clear
		memfs.ClearCacheFiles()
		return nil
	}
	return c.fs.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(d.Name(), ".cache") {
			err = c.fs.Remove(path)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// Len returns the number of cache files in the base directory.
func (c *FileCache[K, V]) Len(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if memfs, ok := c.fs.(*filesystem.MemFS); ok {
		// use memfs optimized count
		return memfs.CountCacheFiles(), nil
	}
	count := 0
	err := c.fs.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(d.Name(), ".cache") {
			count++
		}

		return nil
	})

	return count, err
}

// makeFilename renders a cache filename for key. The canonical key
// bytes (JSON-marshalled, with a Sprintf fallback for unmarshalable
// shapes) are hashed with SHA-256 and rendered as hex — every cache
// file gets a fixed 64-char filename regardless of how long or
// weird the key looks on the wire.
//
// Earlier shape collapsed unsafe filesystem characters (`/`, `\`,
// `:`, `*`, `?`, `"`, `<`, `>`, `|`) to `_`. Two distinct keys could
// collide on the resulting filename:
//
//	"foo/bar" → "foo_bar.cache"
//	"foo_bar" → "foo_bar.cache"   // SAME FILE
//
// On top of that, large keys produced filenames that exceeded
// PATH_MAX on some filesystems. Hashing fixes both: collisions only
// matter at the (astronomically rare) SHA-256 boundary, and the
// length is bounded.
func (c *FileCache[K, V]) makeFilename(key K) string {
	keyBytes, err := json.Marshal(key)
	if err != nil {
		keyBytes = []byte(fmt.Sprintf("%v", key))
	}
	sum := sha256.Sum256(keyBytes)
	return hex.EncodeToString(sum[:]) + ".cache"
}

// Healthy round-trips a probe file through the underlying filesystem.
// Returns nil iff write, read, content match, and cleanup all succeed.
func (c *FileCache[K, V]) Healthy() error {
	const probe = ".health_check"
	want := []byte("health_check")

	if err := c.fs.WriteFile(probe, want, 0644); err != nil {
		return fmt.Errorf("write probe: %w", err)
	}
	got, err := c.fs.ReadFile(probe)
	if err != nil {
		return fmt.Errorf("read probe: %w", err)
	}
	if !bytes.Equal(got, want) {
		return errors.New("probe content mismatch")
	}
	if err := c.fs.Remove(probe); err != nil {
		return fmt.Errorf("remove probe: %w", err)
	}
	return nil
}
