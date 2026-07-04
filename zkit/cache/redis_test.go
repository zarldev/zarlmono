package cache_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zarldev/zarlmono/zkit/cache"
)

// TestRedisCache_ClearRequiresPrefix guards the empty-prefix
// footgun. A RedisCache constructed without WithPrefix would
// previously SCAN "*" and DEL every key in the selected DB on
// Clear — catastrophic when sharing a DB with other data. The
// fix returns ErrClearRequiresPrefix and the caller has to opt
// in to "wipe the whole DB" via direct FLUSHDB.
func TestRedisCache_ClearRequiresPrefix(t *testing.T) {
	t.Parallel()
	// No real Redis needed — the guard fires before any client call.
	c := cache.NewRedisCache[string, int]() // default: empty prefix

	err := c.Clear(t.Context())
	if !errors.Is(err, cache.ErrClearRequiresPrefix) {
		t.Errorf("Clear() with empty prefix err = %v, want ErrClearRequiresPrefix", err)
	}
}

const redisTestAddrEnv = "CACHE_REDIS_TEST_ADDR"

func TestRedisCache_Constructor(t *testing.T) {
	tests := []struct {
		name  string
		opts  []cache.RedisOption[string, int]
		check func(t *testing.T, c any)
	}{
		{
			name: "with default client",
			check: func(t *testing.T, c any) {
				if c == nil {
					t.Error("NewRedisCache() returned nil")
				}
			},
		},
		{
			name: "with custom client",
			opts: []cache.RedisOption[string, int]{
				cache.WithPrefix[string, int]("test"),
				cache.WithClient[string, int](&redis.Client{}),
			},
			check: func(t *testing.T, c any) {
				if c == nil {
					t.Error("NewRedisCache() with custom client returned nil")
				}
			},
		},
		{
			name: "with prefix",
			opts: []cache.RedisOption[string, int]{
				cache.WithPrefix[string, int]("test"),
			},
			check: func(t *testing.T, c any) {
				if c == nil {
					t.Error("NewRedisCache() with prefix returned nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cache.NewRedisCache[string, int](tt.opts...)
			tt.check(t, c)
		})
	}
}

func TestRedisCache_Types(t *testing.T) {
	tests := []struct {
		name  string
		build func() any
	}{
		{
			name: "int to string",
			build: func() any {
				return cache.NewRedisCache[int, string](cache.WithPrefix[int, string]("int-string"))
			},
		},
		{
			name: "string to struct",
			build: func() any {
				type customValue struct{ _ string }
				return cache.NewRedisCache[string, customValue](cache.WithPrefix[string, customValue]("string-struct"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.build()
			if c == nil {
				t.Errorf("NewRedisCache() for %s returned nil", tt.name)
			}
		})
	}
}

func newRedisContractCache(t *testing.T) (cache.Cache[string, int], func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping Redis contract tests in short mode")
	}

	addr := os.Getenv(redisTestAddrEnv)
	if addr == "" {
		t.Skipf("skipping Redis contract tests; set %s to a reachable Redis address", redisTestAddrEnv)
	}

	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	pingCtx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("skipping Redis contract tests; Redis at %s is not reachable: %v", addr, err)
	}

	c := cache.NewRedisCache[string, int](
		cache.WithClient[string, int](client),
		cache.WithPrefix[string, int]("cache-test:"),
	)

	cleanup := func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		_ = c.Clear(ctx)
		_ = client.Close()
	}

	return c, cleanup
}
