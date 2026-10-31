package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestCodexExplicitContextCapPrecedesAccountProbe(t *testing.T) {
	t.Parallel()

	settings := newJudgeTestSettings(t)
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{
		Name:          "openai-codex",
		AdapterType:   backends.AdapterTypes.OAUTHOPENAICODEX,
		BaseURL:       "https://registry-override.invalid",
		DefaultModel:  "gpt-5.5",
		ContextWindow: 204000,
		Enabled:       true,
		Builtin:       true,
	}}))

	// Default-account probe endpoint consistency is enforced in production matching
	// BuildProvider; endpoint override isn't supported for the Codex engine route.
	for _, endpoint := range []string{"", "https://spec-override.invalid"} {
		got := settings.ContextWindow(t.Context(), engine.ProviderSpec{
			Name:    "openai-codex",
			Model:   "gpt-5.5",
			BaseURL: endpoint,
		})
		if got != 204000 {
			t.Fatalf("ContextWindow() = %d, want 204000", got)
		}
	}
}
