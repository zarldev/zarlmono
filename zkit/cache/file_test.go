package cache_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/cache"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

func TestFileCache_Constructor(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *cache.FileCache[string, int]
	}{
		{
			name: "with default temp directory",
			setup: func() *cache.FileCache[string, int] {
				return cache.NewFileCache[string, int]()
			},
		},
		{
			name: "with custom directory",
			setup: func() *cache.FileCache[string, int] {
				return cache.NewFileCache[string, int](cache.WithOSFileSystem[string, int](t.TempDir()))
			},
		},
		{
			name: "with in-memory filesystem",
			setup: func() *cache.FileCache[string, int] {
				return cache.NewFileCache[string, int](cache.WithFileSystem[string, int](filesystem.NewMemFS()))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.setup()
			if c == nil {
				t.Error("NewFileCache() returned nil")
			}
		})
	}
}

func TestFileCache_Persistence(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value int
		check func(t *testing.T, c1, c2 *cache.FileCache[string, int])
	}{
		{
			name:  "data persists across instances",
			key:   "persistent",
			value: 123,
			check: func(t *testing.T, c1, c2 *cache.FileCache[string, int]) {
				ctx := t.Context()
				c1.Set(ctx, "persistent", 123)

				if got, _ := c1.Len(ctx); got != 1 {
					t.Errorf("first instance Len() = %v, want 1", got)
				}

				value, err := c2.Get(ctx, "persistent")
				if err != nil {
					t.Errorf("second instance Get() error = %v, want nil", err)
					return
				}

				if value != 123 {
					t.Errorf("second instance Get() = %v, want 123", value)
				}

				if got, _ := c2.Len(ctx); got != 1 {
					t.Errorf("second instance Len() = %v, want 1", got)
				}
			},
		},
		{
			name:  "multiple values persist",
			key:   "multiple",
			value: 456,
			check: func(t *testing.T, c1, c2 *cache.FileCache[string, int]) {
				ctx := t.Context()
				c1.Set(ctx, "key1", 1)
				c1.Set(ctx, "key2", 2)
				c1.Set(ctx, "key3", 3)

				if got, _ := c2.Len(ctx); got != 3 {
					t.Errorf("second instance Len() = %v, want 3", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			c1 := cache.NewFileCache[string, int](cache.WithOSFileSystem[string, int](tmpDir))
			c2 := cache.NewFileCache[string, int](cache.WithOSFileSystem[string, int](tmpDir))

			tt.check(t, c1, c2)
		})
	}
}

func TestFileCache_FileHandling(t *testing.T) {
	tests := []struct {
		name  string
		setup func(c *cache.FileCache[string, string], dir string)
		check func(t *testing.T, c *cache.FileCache[string, string], dir string)
	}{
		{
			name: "files created with .cache extension",
			setup: func(c *cache.FileCache[string, string], dir string) {
				c.Set(t.Context(), "test", "value")
			},
			check: func(t *testing.T, c *cache.FileCache[string, string], dir string) {
				files, err := filepath.Glob(filepath.Join(dir, "*.cache"))
				if err != nil {
					t.Errorf("glob cache files: %v", err)
					return
				}

				if len(files) != 1 {
					t.Errorf("found %d cache files, want 1", len(files))
				}
			},
		},
		{
			name: "special characters sanitized",
			setup: func(c *cache.FileCache[string, string], dir string) {
				c.Set(t.Context(), "key/with\\special:chars*?\"<>|", "value")
			},
			check: func(t *testing.T, c *cache.FileCache[string, string], dir string) {
				value, err := c.Get(t.Context(), "key/with\\special:chars*?\"<>|")
				if err != nil {
					t.Errorf("Get() with special chars error = %v, want nil", err)
					return
				}

				if value != "value" {
					t.Errorf("Get() with special chars = %v, want value", value)
				}
			},
		},
		{
			name: "multiple files created",
			setup: func(c *cache.FileCache[string, string], dir string) {
				ctx := t.Context()
				c.Set(ctx, "file1", "value1")
				c.Set(ctx, "file2", "value2")
				c.Set(ctx, "file3", "value3")
			},
			check: func(t *testing.T, c *cache.FileCache[string, string], dir string) {
				files, err := filepath.Glob(filepath.Join(dir, "*.cache"))
				if err != nil {
					t.Errorf("glob cache files: %v", err)
					return
				}

				if len(files) != 3 {
					t.Errorf("found %d cache files, want 3", len(files))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			c := cache.NewFileCache[string, string](cache.WithOSFileSystem[string, string](tmpDir))

			tt.setup(c, tmpDir)
			tt.check(t, c, tmpDir)
		})
	}
}

// TestFileCache_KeysDoNotCollideViaCharSubstitution guards the
// hashed-filename fix. Earlier the cache replaced filesystem-unsafe
// characters with `_`, so "foo/bar" and "foo_bar" both became
// "foo_bar.cache" — two distinct keys, one cache file. The SHA-256
// hash means each key has its own 64-char hex filename.
func TestFileCache_KeysDoNotCollideViaCharSubstitution(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := cache.NewFileCache[string, string](cache.WithFileSystem[string, string](filesystem.NewMemFS()))

	if err := c.Set(ctx, "foo/bar", "slash"); err != nil {
		t.Fatalf("Set foo/bar: %v", err)
	}
	if err := c.Set(ctx, "foo_bar", "underscore"); err != nil {
		t.Fatalf("Set foo_bar: %v", err)
	}

	got, err := c.Get(ctx, "foo/bar")
	if err != nil {
		t.Fatalf("Get foo/bar: %v", err)
	}
	if got != "slash" {
		t.Errorf(
			"foo/bar = %q, want %q (the underscore key would have shadowed it under the old filename rule)",
			got,
			"slash",
		)
	}

	got, err = c.Get(ctx, "foo_bar")
	if err != nil {
		t.Fatalf("Get foo_bar: %v", err)
	}
	if got != "underscore" {
		t.Errorf("foo_bar = %q, want %q", got, "underscore")
	}

	if n, _ := c.Len(ctx); n != 2 {
		t.Errorf("Len = %d, want 2 (two distinct cache files)", n)
	}
}

// TestFileCache_HandlesVeryLongKeys verifies the hashed-filename fix
// also caps filename length — earlier shape produced filenames as
// long as the marshalled key, which exceeded PATH_MAX on some
// filesystems. A 4 KiB key now hashes to a 68-byte filename
// (64 hex + ".cache").
func TestFileCache_HandlesVeryLongKeys(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := cache.NewFileCache[string, string](cache.WithFileSystem[string, string](filesystem.NewMemFS()))

	bigKey := strings.Repeat("a", 4096)
	if err := c.Set(ctx, bigKey, "ok"); err != nil {
		t.Fatalf("Set big key: %v", err)
	}
	got, err := c.Get(ctx, bigKey)
	if err != nil {
		t.Fatalf("Get big key: %v", err)
	}
	if got != "ok" {
		t.Errorf("got = %q, want ok", got)
	}
}

// TestNewPersistentFileCache_FailsLoudWhenDirCantOpen guards the
// explicit-persistence constructor. NewFileCache silently downgrades
// to MemFS when the OS path is unusable; NewPersistentFileCache must
// return ErrPersistenceRequired so callers whose correctness depends
// on durability find out at startup.
func TestNewPersistentFileCache_FailsLoudWhenDirCantOpen(t *testing.T) {
	t.Parallel()
	// /proc on Linux is read-only — MkdirAll under it fails. On
	// non-Linux platforms the test still constructs a path that
	// almost certainly won't be creatable.
	c, err := cache.NewPersistentFileCache[string, string]("/proc/forbidden-cache")
	if err == nil {
		// Some sandboxed test environments may actually allow this
		// path; if so, fall back to verifying the success case has
		// IsPersistent=true.
		if c == nil || !c.IsPersistent() {
			t.Fatal("NewPersistentFileCache returned (nil cache, nil err) — contract violation")
		}
		t.Skipf("environment unexpectedly allows /proc writes; persistence-required path not exercisable here")
	}
	if !errors.Is(err, cache.ErrPersistenceRequired) {
		t.Errorf("err = %v, want ErrPersistenceRequired", err)
	}
}
