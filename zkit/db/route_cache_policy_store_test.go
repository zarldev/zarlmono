package db_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestRouteCachePolicyStoreExactKeysAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	p := backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M, System: true, Tools: true}
	alias := "  Case/Ω  "
	if _, err := s.GetRouteCachePolicy(t.Context(), "anthropic", alias); !errors.Is(err, backends.ErrRouteCachePolicyNotFound) {
		t.Fatal(err)
	}
	if err := s.SetRouteCachePolicy(t.Context(), "anthropic", alias, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteCachePolicy(t.Context(), "anthropic", "  case/Ω  ", backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRouteCachePolicy(t.Context(), "anthropic", alias)
	if err != nil || got != p {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	if err := s.DeleteRouteCachePolicy(t.Context(), "anthropic", alias); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRouteCachePolicy(t.Context(), "anthropic", alias); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetRouteCachePolicy(t.Context(), "anthropic", "  case/Ω  ")
	if err != nil || got.Mode != backends.RouteCacheModes.CACHEDEFAULT {
		t.Fatalf("distinct alias: %+v %v", got, err)
	}
	for _, key := range []struct{ provider, alias string }{{"", alias}, {"anthropic", ""}, {"\xff", alias}, {"anthropic", "\xff"}} {
		if err := s.SetRouteCachePolicy(t.Context(), key.provider, key.alias, p); !errors.Is(err, backends.ErrInvalidRouteCachePolicy) {
			t.Fatal(err)
		}
	}
	if err := s.SetRouteCachePolicy(t.Context(), "anthropic", alias, backends.RouteCachePolicy{}); !errors.Is(err, backends.ErrInvalidRouteCachePolicy) {
		t.Fatal(err)
	}
}

func TestRouteCachePolicyStoreCorruptRowsAndReadFailure(t *testing.T) {
	s, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.DB().ExecContext(t.Context(), "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	// The writer is a single connection; corrupt rows bypass constraints to exercise
	// strict read validation even for files written by older or external software.
	cases := []struct {
		name, column, value string
		want                error
	}{
		{"version", "version", "2", backends.ErrRouteCachePolicyVersion},
		{"version type", "version", "'1x'", backends.ErrInvalidRouteCachePolicy},
		{"mode", "mode", "'INHERIT'", backends.ErrInvalidRouteCachePolicy},
		{"inherited", "mode", "'inherit'", backends.ErrInvalidRouteCachePolicy},
		{"mode type", "mode", "2", backends.ErrInvalidRouteCachePolicy},
		{"ttl", "static_ttl", "'6m'", backends.ErrInvalidRouteCachePolicy},
		{"ttl type", "rolling_ttl", "3", backends.ErrInvalidRouteCachePolicy},
		{"flag", "system_marker", "2", backends.ErrInvalidRouteCachePolicy},
		{"flag type", "tool_marker", "'x'", backends.ErrInvalidRouteCachePolicy},
		{"shape", "tool_marker", "1", backends.ErrInvalidRouteCachePolicy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.DB().ExecContext(t.Context(), "INSERT OR REPLACE INTO llm_route_cache_policies VALUES ('anthropic','a',1,'default','default','default',0,0)")
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.DB().ExecContext(t.Context(), "UPDATE llm_route_cache_policies SET "+tc.column+" = "+tc.value+" WHERE provider='anthropic' AND alias='a'")
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.GetRouteCachePolicy(t.Context(), "anthropic", "a")
			if !errors.Is(err, tc.want) {
				t.Fatalf("read error: %v, want %v", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "1x") {
				t.Fatalf("raw value leaked: %v", err)
			}
		})
	}
	if _, err := s.DB().ExecContext(t.Context(), "DROP TABLE llm_route_cache_policies"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRouteCachePolicy(t.Context(), "anthropic", "a"); err == nil || errors.Is(err, backends.ErrRouteCachePolicyNotFound) {
		t.Fatalf("read failure incorrectly absent: %v", err)
	}
}
