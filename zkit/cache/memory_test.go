package cache_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/cache"
)

func TestMemoryCache_Constructor(t *testing.T) {
	tests := []struct {
		name  string
		build func() any
		check func(t *testing.T, c any)
	}{
		{
			name: "string to int",
			build: func() any {
				return cache.NewMemoryCache[string, int]()
			},
			check: func(t *testing.T, c any) {
				mc := c.(*cache.MemoryCache[string, int])
				if mc == nil {
					t.Error("NewMemoryCache() returned nil")
				}
				if got, _ := mc.Len(t.Context()); got != 0 {
					t.Errorf("Len() = %v, want 0", got)
				}
			},
		},
		{
			name: "string to string",
			build: func() any {
				return cache.NewMemoryCache[string, string]()
			},
			check: func(t *testing.T, c any) {
				if c == nil {
					t.Error("NewMemoryCache[string, string]() returned nil")
				}
			},
		},
		{
			name: "int to bool",
			build: func() any {
				return cache.NewMemoryCache[int, bool]()
			},
			check: func(t *testing.T, c any) {
				if c == nil {
					t.Error("NewMemoryCache[int, bool]() returned nil")
				}
			},
		},
		{
			name: "custom struct types",
			build: func() any {
				type customKey struct{ _ int }
				type customValue struct{ _ string }
				return cache.NewMemoryCache[customKey, customValue]()
			},
			check: func(t *testing.T, c any) {
				if c == nil {
					t.Error("NewMemoryCache[customKey, customValue]() returned nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.build()
			tt.check(t, c)
		})
	}
}
