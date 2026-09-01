package backends_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestRegistryBuildsPublicProviders(t *testing.T) {
	t.Parallel()

	registry := backends.NewRegistry()
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "llamacpp"},
		{name: "deepseek", key: "test-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, err := registry.BuildWithConfig(t.Context(), tc.name, backends.BuildConfig{APIKey: tc.key})
			if err != nil {
				t.Fatalf("BuildWithConfig(%q): %v", tc.name, err)
			}
			if got := provider.Name(); got != tc.name {
				t.Fatalf("BuildWithConfig(%q).Name() = %q, want %q", tc.name, got, tc.name)
			}
		})
	}
}
