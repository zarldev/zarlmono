package backends_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestBuildValidatesRequiredCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"openai", llm.ErrInvalidAPIKey}, {"anthropic", llm.ErrInvalidAPIKey}, {"deepseek", llm.ErrInvalidAPIKey}, {"gemini", llm.ErrInvalidAPIKey}, {"llamacpp", nil}, {"ollama", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := backends.NewRegistry()
			provider, err := reg.BuildWithConfig(t.Context(), tc.name, backends.BuildConfig{})
			if !errors.Is(err, tc.err) {
				t.Fatalf("BuildWithConfig: %v, want %v", err, tc.err)
			}
			if tc.err == nil && provider.Name() != tc.name {
				t.Errorf("Name = %q, want %q", provider.Name(), tc.name)
			}
		})
	}
}
