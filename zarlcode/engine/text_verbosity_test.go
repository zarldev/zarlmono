package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestTextVerbosityResolutionAndProviderGating(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyTextVerbosity, "high"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		target engine.ProviderSpec
		want   string
	}{
		{engine.ProviderSpec{Name: backends.NameOpenAICodex.String(), Model: "gpt-5.6"}, "high"},
		{engine.ProviderSpec{Name: llm.LLMProviders.OPENAI.String(), Model: "gpt-5.6-sol"}, "high"},
		{engine.ProviderSpec{Name: llm.LLMProviders.OPENAI.String(), Model: "gpt-6-astra"}, "high"},
		{engine.ProviderSpec{Name: llm.LLMProviders.OPENAI.String(), Model: "gpt-4o"}, ""},
		{engine.ProviderSpec{Name: llm.LLMProviders.ANTHROPIC.String(), Model: "claude"}, ""},
	}
	for _, tc := range cases {
		if got := settings.TextVerbosity(t.Context(), tc.target); got != tc.want {
			t.Errorf("TextVerbosity(%+v) = %q, want %q", tc.target, got, tc.want)
		}
	}

	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyTextVerbosity, "verbose"); err != nil {
		t.Fatal(err)
	}
	if got := settings.TextVerbosity(t.Context(), cases[0].target); got != "" {
		t.Errorf("invalid verbosity = %q, want empty", got)
	}
}

func TestLegacyCodexEffortResolutionIsPreserved(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyCodexEffort, "xhigh"); err != nil {
		t.Fatal(err)
	}
	got := settings.ActiveProvider(t.Context(), engine.ProviderSpec{Name: backends.NameOpenAICodex.String(), Model: "gpt-5.6"})
	if got.CodexEffort != "xhigh" {
		t.Fatalf("CodexEffort = %q, want legacy xhigh", got.CodexEffort)
	}
}

func TestCodexEffortIsValidatedForSelectedModel(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyCodexEffort, "max"); err != nil {
		t.Fatal(err)
	}

	codex := backends.NameOpenAICodex.String()
	if got := settings.CodexEffort(t.Context(), engine.ProviderSpec{Name: codex, Model: "gpt-6-astra"}); got != "max" {
		t.Fatalf("gpt-6-astra effort = %q, want max", got)
	}
	if got := settings.CodexEffort(t.Context(), engine.ProviderSpec{Name: codex, Model: "gpt-5.6"}); got != "max" {
		t.Fatalf("gpt-5.6 effort = %q, want max", got)
	}
	if got := settings.CodexEffort(t.Context(), engine.ProviderSpec{Name: codex, Model: "gpt-5.4-mini"}); got != "" {
		t.Fatalf("mini effort = %q, want omitted", got)
	}
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyCodexEffort, "turbo"); err != nil {
		t.Fatal(err)
	}
	if got := settings.CodexEffort(t.Context(), engine.ProviderSpec{Name: codex, Model: "gpt-5.6"}); got != "" {
		t.Fatalf("manual invalid effort = %q, want omitted", got)
	}
}
