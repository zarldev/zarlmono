package engine_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

func TestGPT6RouteDefaultsPreserveGPT56Pins(t *testing.T) {
	for _, tc := range []struct {
		providerKey, modelKey, oldModel, newModel string
	}{
		{prefs.KeyCompactProvider, prefs.KeyCompactModel, "gpt-5.6-sol", "gpt-6-sol"},
		{prefs.KeySpawnDefaultImplementProvider, prefs.KeySpawnDefaultImplementModel, "gpt-5.6-sol", "gpt-6-sol"},
		{prefs.KeySpawnDefaultVerifyProvider, prefs.KeySpawnDefaultVerifyModel, "gpt-5.6-luna", "gpt-6-luna"},
	} {
		t.Run(tc.providerKey, func(t *testing.T) {
			s := newJudgeTestSettings(t)
			ctx := t.Context()
			for _, scope := range []prefs.Scope{prefs.ScopeGlobal, prefs.ScopeWorkspace} {
				for key, value := range map[string]string{tc.providerKey: "openai-codex", tc.modelKey: tc.oldModel} {
					if err := s.Svc.SetSetting(ctx, scope, key, value); err != nil {
						t.Fatal(err)
					}
				}
				if got := s.ModelTarget(ctx, tc.providerKey); got != (engine.ProviderSpec{Name: "openai-codex", Model: tc.oldModel}) {
					t.Fatalf("%s pin changed to %+v", scope, got)
				}
			}
			for _, key := range []string{tc.providerKey, tc.modelKey} {
				if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, key, prefs.ActiveModelTarget); err != nil {
					t.Fatal(err)
				}
			}
			if got := s.ModelTarget(ctx, tc.providerKey); got != (engine.ProviderSpec{}) {
				t.Fatalf("active reset ignored: %+v", got)
			}
			for _, scope := range []prefs.Scope{prefs.ScopeWorkspace, prefs.ScopeGlobal} {
				for _, key := range []string{tc.providerKey, tc.modelKey} {
					if err := s.Svc.DeleteSetting(ctx, scope, key); err != nil {
						t.Fatal(err)
					}
				}
				want := tc.oldModel
				if scope == prefs.ScopeGlobal {
					want = tc.newModel
				}
				if got := s.ModelTarget(ctx, tc.providerKey); got != (engine.ProviderSpec{Name: "openai-codex", Model: want}) {
					t.Fatalf("after clearing %s: %+v, want %s", scope, got, want)
				}
			}
			for _, key := range []string{tc.providerKey, tc.modelKey} {
				if _, err := s.Svc.GetSetting(ctx, prefs.ScopeEffective, key); !errors.Is(err, prefs.ErrNotFound) {
					t.Fatalf("default read persisted %s: %v", key, err)
				}
			}
		})
	}
}

func TestGPT6CodexEffortAndVerbosity(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyTextVerbosity, "high"); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"} {
		target := engine.ProviderSpec{Name: "openai-codex", Model: model}
		if got := s.CodexEffort(ctx, target); got != "high" {
			t.Fatalf("%s default effort = %q", model, got)
		}
		if got := s.TextVerbosity(ctx, target); got != "high" {
			t.Fatalf("%s verbosity = %q", model, got)
		}
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyCodexEffort, "max"); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"} {
		if got := s.CodexEffort(ctx, engine.ProviderSpec{Name: "openai-codex", Model: model}); got != "max" {
			t.Fatalf("%s explicit effort = %q", model, got)
		}
	}
}
