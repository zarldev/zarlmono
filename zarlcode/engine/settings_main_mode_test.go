package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

func TestMainModeTargetInheritanceAndOverrides(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()
	active := engine.ProviderSpec{
		Name: "llamacpp", Model: "current-model",
		BaseURL: "http://127.0.0.1:8081", APIKey: "current-key",
	}

	if got := s.MainModeTarget(ctx, true, active); got != active {
		t.Fatalf("unset Plan route = %#v, want active unchanged", got)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyMainPlanProvider, prefs.ActiveModelTarget); err != nil {
		t.Fatal(err)
	}
	if got := s.MainModeTarget(ctx, true, active); got != active {
		t.Fatalf("explicit active provider = %#v, want active unchanged", got)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyMainPlanModel, "plan-model"); err != nil {
		t.Fatal(err)
	}
	want := active
	want.Model = "plan-model"
	if got := s.MainModeTarget(ctx, true, active); got != want {
		t.Fatalf("model-only Plan route = %#v, want %#v", got, want)
	}
	if got := s.MainModeTarget(ctx, false, active); got != active {
		t.Fatalf("Plan settings affected Build route: %#v", got)
	}
}

func TestMainModeTargetProviderDefaultsAndCredentialIsolation(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()
	active := engine.ProviderSpec{
		Name: "llamacpp", Model: "current-model",
		BaseURL: "http://127.0.0.1:8081", APIKey: "current-key",
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyMainBuildProvider, "deepseek"); err != nil {
		t.Fatal(err)
	}
	got := s.MainModeTarget(ctx, false, active)
	if got.Name != "deepseek" || got.Model != "deepseek-flash" {
		t.Fatalf("provider-only Build route = %#v, want deepseek default model", got)
	}
	if got.BaseURL != "" || got.APIKey != "" {
		t.Fatalf("active credentials leaked across providers: %#v", got)
	}

	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyMainBuildModel, prefs.ActiveModelTarget); err != nil {
		t.Fatal(err)
	}
	got = s.MainModeTarget(ctx, false, active)
	if got.Name != "deepseek" || got.Model != active.Model {
		t.Fatalf("explicit active model = %#v, want current model on selected provider", got)
	}

	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyMainBuildModel, "custom-build"); err != nil {
		t.Fatal(err)
	}
	got = s.MainModeTarget(ctx, false, active)
	if got.Name != "deepseek" || got.Model != "custom-build" {
		t.Fatalf("explicit provider/model route = %#v", got)
	}
}

func TestMainModeTargetLeavesInvalidPersistedTargetForProviderBuilder(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyMainPlanProvider, "not-a-provider"); err != nil {
		t.Fatal(err)
	}
	target := s.MainModeTarget(ctx, true, engine.ProviderSpec{Name: "llamacpp", Model: "current"})
	if target.Name != "not-a-provider" {
		t.Fatalf("persisted provider was repaired: %#v", target)
	}
	if _, err := engine.BuildProvider(ctx, s.Registry, s.Svc, target); err == nil {
		t.Fatal("BuildProvider accepted invalid persisted provider")
	}
}
