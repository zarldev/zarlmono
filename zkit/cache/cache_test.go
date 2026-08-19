package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/cache"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

func TestCache_Contract(t *testing.T) {
	tests := []struct {
		name     string
		newCache func(t *testing.T) (cache.Cache[string, int], func())
	}{
		{
			name: "MemoryCache",
			newCache: func(t *testing.T) (cache.Cache[string, int], func()) {
				return cache.NewMemoryCache[string, int](), func() {}
			},
		},
		{
			name:     "RedisCache",
			newCache: newRedisContractCache,
		},
		{
			name: "FileCache",
			newCache: func(t *testing.T) (cache.Cache[string, int], func()) {
				c := cache.NewFileCache[string, int](cache.WithFileSystem[string, int](filesystem.NewMemFS()))

				return c, func() {}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, cleanup := tt.newCache(t)
			defer cleanup()

			testCacheContract(t, c)
		})
	}
}

func testCacheContract(t *testing.T, c cache.Cache[string, int]) {
	t.Helper()
	ctx := t.Context()

	t.Run("canceled and expired contexts", func(t *testing.T) {
		contexts := []struct {
			name string
			new  func() (context.Context, context.CancelFunc)
		}{
			{
				name: "canceled",
				new: func() (context.Context, context.CancelFunc) {
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					return ctx, func() {}
				},
			},
			{
				name: "deadline exceeded",
				new: func() (context.Context, context.CancelFunc) {
					return context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				},
			},
		}
		for _, tc := range contexts {
			t.Run(tc.name, func(t *testing.T) {
				operationCtx, cancel := tc.new()
				defer cancel()
				want := operationCtx.Err()
				if err := c.Set(operationCtx, "key", 1); !errors.Is(err, want) {
					t.Errorf("Set() error = %v, want %v", err, want)
				}
				if _, err := c.Get(operationCtx, "key"); !errors.Is(err, want) {
					t.Errorf("Get() error = %v, want %v", err, want)
				}
				if _, err := c.Delete(operationCtx, "key"); !errors.Is(err, want) {
					t.Errorf("Delete() error = %v, want %v", err, want)
				}
				if _, err := c.Len(operationCtx); !errors.Is(err, want) {
					t.Errorf("Len() error = %v, want %v", err, want)
				}
				if err := c.Clear(operationCtx); !errors.Is(err, want) {
					t.Errorf("Clear() error = %v, want %v", err, want)
				}
			})
		}
	})

	t.Run("Get operations", func(t *testing.T) {
		tests := []struct {
			name  string
			setup func()
			key   string
			want  int
			error error
		}{
			{
				name:  "get from empty cache",
				setup: func() {},
				key:   "missing",
				error: cache.ErrNotFound,
			},
			{
				name: "get existing key",
				setup: func() {
					c.Set(ctx, "exists", 123)
				},
				key:  "exists",
				want: 123,
			},
			{
				name: "get after overwrite",
				setup: func() {
					c.Set(ctx, "key", 100)
					c.Set(ctx, "key", 200)
				},
				key:  "key",
				want: 200,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c.Clear(ctx)
				tt.setup()

				got, err := c.Get(ctx, tt.key)
				if !errors.Is(err, tt.error) {
					t.Errorf("Get() error = %v, want %v", err, tt.error)
					return
				}

				if err == nil && got != tt.want {
					t.Errorf("Get() = %v, want %v", got, tt.want)
				}
			})
		}
	})

	t.Run("Set operations", func(t *testing.T) {
		tests := []struct {
			name   string
			ops    func()
			verify func(t *testing.T)
		}{
			{
				name: "set single value",
				ops: func() {
					c.Set(ctx, "single", 1)
				},
				verify: func(t *testing.T) {
					if got, _ := c.Len(ctx); got != 1 {
						t.Errorf("Len() = %v, want 1", got)
					}
				},
			},
			{
				name: "set multiple values",
				ops: func() {
					c.Set(ctx, "a", 1)
					c.Set(ctx, "b", 2)
					c.Set(ctx, "c", 3)
				},
				verify: func(t *testing.T) {
					if got, _ := c.Len(ctx); got != 3 {
						t.Errorf("Len() = %v, want 3", got)
					}
				},
			},
			{
				name: "overwrite preserves count",
				ops: func() {
					c.Set(ctx, "key", 1)
					c.Set(ctx, "key", 2)
				},
				verify: func(t *testing.T) {
					if got, _ := c.Len(ctx); got != 1 {
						t.Errorf("Len() = %v, want 1", got)
					}

					val, _ := c.Get(ctx, "key")
					if val != 2 {
						t.Errorf("Get() = %v, want 2", val)
					}
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c.Clear(ctx)
				tt.ops()
				tt.verify(t)
			})
		}
	})

	t.Run("Delete operations", func(t *testing.T) {
		tests := []struct {
			name     string
			setup    func()
			key      string
			want     bool
			lenAfter int
		}{
			{
				name:     "delete from empty cache",
				setup:    func() {},
				key:      "missing",
				want:     false,
				lenAfter: 0,
			},
			{
				name: "delete existing key",
				setup: func() {
					c.Set(ctx, "exists", 123)
				},
				key:      "exists",
				want:     true,
				lenAfter: 0,
			},
			{
				name: "delete one of many",
				setup: func() {
					c.Set(ctx, "a", 1)
					c.Set(ctx, "b", 2)
					c.Set(ctx, "c", 3)
				},
				key:      "b",
				want:     true,
				lenAfter: 2,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c.Clear(ctx)
				tt.setup()

				got, _ := c.Delete(ctx, tt.key)
				if got != tt.want {
					t.Errorf("Delete() = %v, want %v", got, tt.want)
				}

				if length, _ := c.Len(ctx); length != tt.lenAfter {
					t.Errorf("Len() after Delete = %v, want %v", length, tt.lenAfter)
				}
			})
		}
	})

	t.Run("Clear operations", func(t *testing.T) {
		tests := []struct {
			name  string
			setup func()
		}{
			{
				name:  "clear empty cache",
				setup: func() {},
			},
			{
				name: "clear cache with items",
				setup: func() {
					c.Set(ctx, "a", 1)
					c.Set(ctx, "b", 2)
					c.Set(ctx, "c", 3)
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tt.setup()
				c.Clear(ctx)

				if got, _ := c.Len(ctx); got != 0 {
					t.Errorf("Len() after Clear = %v, want 0", got)
				}

				_, err := c.Get(ctx, "a")
				if !errors.Is(err, cache.ErrNotFound) {
					t.Errorf("Get() after Clear error = %v, want %v", err, cache.ErrNotFound)
				}
			})
		}
	})
}
