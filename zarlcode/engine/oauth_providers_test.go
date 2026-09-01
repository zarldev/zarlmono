package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestBuildProviderRequiresCredentialServiceForOAuthBackends(t *testing.T) {
	for _, spec := range []engine.ProviderSpec{
		{Name: "openai-codex", Model: "gpt-5"},
		{Name: "claude-code", Model: "sonnet"},
	} {
		t.Run(spec.Name, func(t *testing.T) {
			if _, err := engine.BuildProvider(t.Context(), nil, nil, spec); err == nil {
				t.Fatal("BuildProvider() error = nil, want unavailable credential service")
			}
		})
	}
}

func TestBuildProviderConstructsOAuthBackendsFromSettingsService(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())

	codex, err := engine.BuildProvider(t.Context(), settings.Registry, settings.Svc, engine.ProviderSpec{
		Name:        "openai-codex",
		Model:       "gpt-5",
		CodexEffort: "high",
	})
	if err != nil {
		t.Fatalf("build openai-codex: %v", err)
	}
	if _, ok := codex.(*openaicodex.Provider); !ok {
		t.Fatalf("openai-codex provider type = %T", codex)
	}

	claude, err := engine.BuildProvider(t.Context(), settings.Registry, settings.Svc, engine.ProviderSpec{
		Name:  "claude-code",
		Model: "opus",
	})
	if err != nil {
		t.Fatalf("build claude-code: %v", err)
	}
	if _, ok := claude.(*claudecode.Provider); !ok {
		t.Fatalf("claude-code provider type = %T", claude)
	}
}
