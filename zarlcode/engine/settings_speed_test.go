package engine_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSpeedVariants(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		provider, model string
		want            []string
	}{
		{"openai", "gpt-6-astra", []string{"standard", "fast", "ultrafast"}},
		{"openai-codex", "gpt-6-astra-max", []string{"standard", "fast", "ultrafast"}},
		{"openai", "gpt-6.1-sol", []string{"standard", "fast"}},
		{"openai-codex", "gpt-6.1-sol-high", []string{"standard", "fast"}},
		{"openai", "gpt-6-sol", []string{"standard", "fast"}},
		{"openai-codex", "gpt-6-luna", []string{"standard", "fast"}},
		{"openai-codex", "gpt-5.6", []string{"standard", "fast"}},
		{"openai", "gpt-5.5", []string{"standard", "fast"}},
		{"openai", "gpt-6.1-unknown", nil},
		{"openai", "gpt-4o", nil},
		{"anthropic", "gpt-6-astra", nil},
		{"llamacpp", "gpt-6.1-sol", nil},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			t.Parallel()
			target := engine.ProviderSpec{Name: tc.provider, Model: tc.model}
			got := engine.SpeedVariants(target)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("variants = %v, want %v", got, tc.want)
			}
			if len(got) > 0 {
				got[0] = "mutated"
				if !slices.Equal(engine.SpeedVariants(target), tc.want) {
					t.Fatal("caller mutated shared variants")
				}
			}
		})
	}
}

func TestServiceTierScopesAndModelGates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ value, astra, sol string }{
		{"standard", "default", "default"}, {"fast", "fast", "fast"},
		{"ultrafast", "ultrafast", ""}, {"(auto)", "", ""},
		{"invalid", "", ""}, {" FAST ", "fast", "fast"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()
			store, err := db.Open(t.Context(), t.TempDir()+"/state.db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			s := engine.NewSettings(store, nil, nil, t.TempDir())
			astra := engine.ProviderSpec{Name: "openai", Model: "gpt-6-astra"}
			sol := engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6.1-sol"}
			if got := s.ServiceTier(t.Context(), astra); got != "" {
				t.Fatalf("unset = %q", got)
			}
			if err := s.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyServiceTier, "fast"); err != nil {
				t.Fatal(err)
			}
			if got := s.ServiceTier(t.Context(), sol); got != "fast" {
				t.Fatalf("global fallback = %q", got)
			}
			if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyServiceTier, tc.value); err != nil {
				t.Fatal(err)
			}
			if got := s.ServiceTier(t.Context(), astra); got != tc.astra {
				t.Errorf("astra = %q, want %q", got, tc.astra)
			}
			if got := s.ServiceTier(t.Context(), sol); got != tc.sol {
				t.Errorf("sol = %q, want %q", got, tc.sol)
			}
			if got := s.ServiceTier(t.Context(), engine.ProviderSpec{Name: "anthropic", Model: "gpt-6-astra"}); got != "" {
				t.Errorf("unrelated provider inherited speed: %q", got)
			}
		})
	}
}

func TestLiveRunnerAppliesSpeedOnNextTurn(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := engine.NewSettings(store, nil, nil, ws.Root())
	provider := &requestRecordingProvider{}
	live := engine.NewLiveRunner(provider, ws, "gpt-6.1-sol", engine.WithSettings(s))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6.1-sol"}})
	for _, tc := range []struct{ value, want string }{
		{"fast", "fast"}, {"standard", "default"}, {"(auto)", ""}, {"ultrafast", ""},
	} {
		if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyServiceTier, tc.value); err != nil {
			t.Fatal(err)
		}
		if err := live.RunTurn(t.Context(), tc.value); err != nil {
			t.Fatal(err)
		}
		opts := provider.requests[len(provider.requests)-1].Options
		got, present := opts["service_tier"]
		if tc.want == "" && present || tc.want != "" && got != tc.want {
			t.Fatalf("next turn speed = %v (present %v), want %q", got, present, tc.want)
		}
		if opts["reasoning_effort"] != prefs.DefaultCodexEffort {
			t.Fatalf("speed changed reasoning effort: %v", opts)
		}
	}
}
