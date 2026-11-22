package backends_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

type cachePolicyStore struct {
	policies map[string]backends.RouteCachePolicy
	err      error
}

func (s *cachePolicyStore) GetRouteCachePolicy(_ context.Context, _, alias string) (backends.RouteCachePolicy, error) {
	if s.err != nil {
		return backends.RouteCachePolicy{}, s.err
	}
	p, ok := s.policies[alias]
	if !ok {
		return p, backends.ErrRouteCachePolicyNotFound
	}
	return p, nil
}
func (s *cachePolicyStore) SetRouteCachePolicy(_ context.Context, _, alias string, p backends.RouteCachePolicy) error {
	if s.err != nil {
		return s.err
	}
	s.policies[alias] = p
	return nil
}
func (s *cachePolicyStore) DeleteRouteCachePolicy(_ context.Context, _, alias string) error {
	if s.err != nil {
		return s.err
	}
	delete(s.policies, alias)
	return nil
}

func TestRouteCachePolicyShape(t *testing.T) {
	valid := []backends.RouteCachePolicy{{}, {Mode: backends.RouteCacheModes.CACHEDEFAULT}, {Mode: backends.RouteCacheModes.CACHEDISABLED}, {Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M, System: true, Tools: true}}
	for _, p := range valid {
		if err := backends.ValidateRouteCachePolicy(p); err != nil {
			t.Fatalf("valid policy %+v: %v", p, err)
		}
	}
	invalidMode, _ := backends.ParseRouteCacheMode("missing")
	invalidTTL, _ := backends.ParseRouteCacheTTL("missing")
	invalid := []backends.RouteCachePolicy{{Mode: invalidMode}, {Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: invalidTTL}, {Mode: backends.RouteCacheModes.CACHEDEFAULT, System: true}, {Mode: backends.RouteCacheModes.CACHEDISABLED, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}, {Mode: backends.RouteCacheModes.CACHEINHERIT, Tools: true}}
	for _, p := range invalid {
		if err := backends.ValidateRouteCachePolicy(p); !errors.Is(err, backends.ErrInvalidRouteCachePolicy) {
			t.Fatalf("invalid policy %+v: %v", p, err)
		}
	}
}

func TestRegistryRouteCachePolicyFacade(t *testing.T) {
	s := &cachePolicyStore{policies: map[string]backends.RouteCachePolicy{}}
	r := backends.NewRegistry(backends.WithRouteCachePolicyStore(s))
	p := backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}
	alias := "  Case/Ω  "
	if _, err := r.GetRouteCachePolicy(t.Context(), "anthropic", alias); !errors.Is(err, backends.ErrRouteCachePolicyNotFound) {
		t.Fatal(err)
	}
	if err := r.SetRouteCachePolicy(t.Context(), "anthropic", alias, p); err != nil {
		t.Fatal(err)
	}
	if got, err := r.GetRouteCachePolicy(t.Context(), "anthropic", alias); err != nil || got != p {
		t.Fatalf("read = %+v, %v", got, err)
	}
	if _, err := r.GetRouteCachePolicy(t.Context(), "anthropic", "  case/Ω  "); !errors.Is(err, backends.ErrRouteCachePolicyNotFound) {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.DeleteRouteCachePolicy(t.Context(), "anthropic", alias); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.SetRouteCachePolicy(t.Context(), "anthropic", alias, backends.RouteCachePolicy{}); !errors.Is(err, backends.ErrInvalidRouteCachePolicy) {
		t.Fatal(err)
	}
	for _, key := range []struct{ provider, alias string }{{"", alias}, {"anthropic", ""}, {"anthropic", "\xff"}, {"openai", alias}, {"unknown", alias}} {
		if err := r.SetRouteCachePolicy(t.Context(), key.provider, key.alias, p); err == nil {
			t.Fatalf("accepted %+v", key)
		}
	}
	unavailable := backends.NewRegistry()
	if err := unavailable.SetRouteCachePolicy(t.Context(), "anthropic", alias, p); !errors.Is(err, backends.ErrNoStore) {
		t.Fatal(err)
	}
	failure := errors.New("storage unavailable")
	s.err = failure
	if _, err := r.GetRouteCachePolicy(t.Context(), "anthropic", alias); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	s.err = nil
	s.policies[alias] = backends.RouteCachePolicy{}
	if _, err := r.GetRouteCachePolicy(t.Context(), "anthropic", alias); !errors.Is(err, backends.ErrInvalidRouteCachePolicy) {
		t.Fatal(err)
	}
}
