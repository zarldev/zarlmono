package profile_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/profile"
)

// fakeOverrideStore is the minimal in-memory override store for tests.
type fakeOverrideStore struct {
	rows map[profile.Name]profile.Override
}

func (f *fakeOverrideStore) Get(_ context.Context, name profile.Name) (profile.Override, error) {
	if f.rows == nil {
		return profile.Override{}, nil
	}
	return f.rows[name], nil
}

// --- helpers ---

func TestCoalesceStr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		vals []string
		want string
	}{
		{"first non-empty wins", []string{"", "b", "c"}, "b"},
		{"all empty returns empty", []string{"", "", ""}, ""},
		{"single value passthrough", []string{"only"}, "only"},
		{"nil slice returns empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := profile.CoalesceStr(tc.vals...); got != tc.want {
				t.Errorf("CoalesceStr(%v) = %q, want %q", tc.vals, got, tc.want)
			}
		})
	}
}

func TestDeref(t *testing.T) {
	t.Parallel()
	t.Run("string non-nil", func(t *testing.T) {
		t.Parallel()
		s := "hello"
		if got := profile.Deref(&s); got != "hello" {
			t.Errorf("Deref(&\"hello\") = %q, want hello", got)
		}
	})
	t.Run("string nil returns zero", func(t *testing.T) {
		t.Parallel()
		if got := profile.Deref[string](nil); got != "" {
			t.Errorf("Deref[string](nil) = %q, want empty", got)
		}
	})
	t.Run("int32 non-nil", func(t *testing.T) {
		t.Parallel()
		n := int32(42)
		if got := profile.Deref(&n); got != 42 {
			t.Errorf("Deref(&42) = %d, want 42", got)
		}
	})
}

func TestClampDown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		requested int32
		cap       int
		want      int
	}{
		{"requested 0 uses cap", 0, 20, 20},
		{"requested negative uses cap", -1, 20, 20},
		{"requested under cap returns requested", 5, 20, 5},
		{"requested at cap returns cap", 20, 20, 20},
		{"requested over cap clamps to cap", 50, 20, 20},
		{"cap zero returns zero", 5, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := profile.ClampDown(tc.requested, tc.cap); got != tc.want {
				t.Errorf("ClampDown(%d, %d) = %d, want %d", tc.requested, tc.cap, got, tc.want)
			}
		})
	}
}

// --- Registry.Resolve ---

func TestResolve_NoOverride_UsesProfileDefaults(t *testing.T) {
	t.Parallel()

	pr := profile.NewRegistry(profile.Builtin(), &fakeOverrideStore{}, "qwen3-26b")
	got, err := pr.Resolve(context.Background(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Model != "qwen3-26b" {
		t.Errorf("Model = %q, want env fallback qwen3-26b", got.Model)
	}
	if got.PromptPrefix == "" {
		t.Errorf("PromptPrefix should be the researcher default, got empty")
	}
	if got.MaxIterations != 20 {
		t.Errorf("MaxIterations = %d, want 20", got.MaxIterations)
	}
}

func TestResolve_OverrideReplacesModel(t *testing.T) {
	t.Parallel()

	model := "qwen3-31b"
	overrides := &fakeOverrideStore{rows: map[profile.Name]profile.Override{
		profile.NameResearcher: {Model: &model},
	}}

	pr := profile.NewRegistry(profile.Builtin(), overrides, "qwen3-26b")
	got, err := pr.Resolve(context.Background(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Model != "qwen3-31b" {
		t.Errorf("Model = %q, want override qwen3-31b", got.Model)
	}
}

func TestResolve_OverrideReplacesPromptPrefix(t *testing.T) {
	t.Parallel()

	prefix := "you are a parrot"
	overrides := &fakeOverrideStore{rows: map[profile.Name]profile.Override{
		profile.NameDefault: {PromptPrefix: &prefix},
	}}

	pr := profile.NewRegistry(profile.Builtin(), overrides, "model")
	got, err := pr.Resolve(context.Background(), profile.NameDefault)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.PromptPrefix != prefix {
		t.Errorf("PromptPrefix = %q, want override %q", got.PromptPrefix, prefix)
	}
}

func TestResolve_UnknownProfileFallsBackToDefault(t *testing.T) {
	t.Parallel()

	pr := profile.NewRegistry(profile.Builtin(), &fakeOverrideStore{}, "model")
	got, err := pr.Resolve(context.Background(), profile.Name("missing"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Name != profile.NameDefault {
		t.Errorf("Name = %q, want default", got.Name)
	}
}

func TestResolve_MaxIterationsClampedToCap(t *testing.T) {
	t.Parallel()

	custom := []profile.Profile{{
		Name:          "high",
		MaxIterations: 999,
	}}
	pr := profile.NewRegistry(custom, nil, "model")
	got, err := pr.Resolve(context.Background(), "high")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.MaxIterations != profile.DefaultMaxIterations {
		t.Errorf("MaxIterations = %d, want clamped to %d", got.MaxIterations, profile.DefaultMaxIterations)
	}
}
