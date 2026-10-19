package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
)

func TestApplicationBudgetDefaultsAndScopeOverrides(t *testing.T) {
	ctx := t.Context()
	for _, tc := range []struct {
		key  string
		want int
		read func(*engine.Settings) int
	}{
		{prefs.KeyReserveTokens, 5120, func(s *engine.Settings) int { return s.Limits(ctx).ReserveTokens }},
		{prefs.KeyMaxIterations, 900, func(s *engine.Settings) int { return s.Limits(ctx).MaxIterations }},
		{prefs.KeySpawnMaxIterations, 300, func(s *engine.Settings) int { return s.Limits(ctx).SpawnMaxIterations }},
		{prefs.KeySpawnAwaitTimeout, 600, func(s *engine.Settings) int { return s.Limits(ctx).SpawnAwaitTimeout }},
		{prefs.KeySpawnAwaitMaxTimeout, 600, func(s *engine.Settings) int { return s.Limits(ctx).SpawnAwaitMaxTimeout }},
		{prefs.KeySpawnMaxConcurrent, 20, func(s *engine.Settings) int { return s.SpawnMaxConcurrent(ctx) }},
		{prefs.KeySpawnMaxRuntime, 600, func(s *engine.Settings) int { return int(s.SpawnMaxRuntime(ctx) / time.Second) }},
		{prefs.KeySpawnFanoutCap, 80, func(s *engine.Settings) int { return s.SpawnFanoutCap(ctx) }},
		{prefs.KeyFanoutCap, 80, func(s *engine.Settings) int { return s.FanoutCap(ctx) }},
		{prefs.KeyProgramParallel, 16, func(s *engine.Settings) int { return s.ProgrammaticParallelCalls(ctx) }},
		{prefs.KeyToolResultMaxKB, 512, func(s *engine.Settings) int { return s.ToolResultMaxBytes(ctx) / 1024 }},
		{prefs.KeyToolResultMaxLines, 3500, func(s *engine.Settings) int { return s.ToolResultMaxLines(ctx) }},
		{prefs.KeySpawnExploreMaxIterations, 80, func(s *engine.Settings) int { return s.SpawnModes(ctx).Explore.MaxIterations }},
		{prefs.KeySpawnVerifyMaxIterations, 80, func(s *engine.Settings) int { return s.SpawnModes(ctx).Verify.MaxIterations }},
		{prefs.KeySpawnImplementMaxIterations, 300, func(s *engine.Settings) int { return s.SpawnModes(ctx).Implement.MaxIterations }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			s := newJudgeTestSettings(t)
			if got := tc.read(s); got != tc.want {
				t.Fatalf("default = %d, want %d", got, tc.want)
			}
			if _, err := s.Svc.GetSetting(ctx, prefs.ScopeEffective, tc.key); !errors.Is(err, prefs.ErrNotFound) {
				t.Fatalf("reading default persisted a preference: %v", err)
			}
			for _, override := range []struct {
				scope prefs.Scope
				value string
				want  int
			}{
				{prefs.ScopeGlobal, "7", 7},
				{prefs.ScopeWorkspace, "3", 3},
				{prefs.ScopeWorkspace, "0", 0},
				{prefs.ScopeWorkspace, "invalid", tc.want},
			} {
				if err := s.Svc.SetSetting(ctx, override.scope, tc.key, override.value); err != nil {
					t.Fatal(err)
				}
				if got := tc.read(s); got != override.want {
					t.Fatalf("%s override %q = %d, want %d", override.scope, override.value, got, override.want)
				}
			}
			if err := s.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, tc.key); err != nil {
				t.Fatal(err)
			}
			if got := tc.read(s); got != 7 {
				t.Fatalf("cleared workspace = %d, want global 7", got)
			}
		})
	}
}

func TestApplicationSafetyDefaultsAndExplicitProtection(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()
	if s.ShellSandbox(ctx) || !s.ShellGuardOff(ctx) || s.ReadBeforeWriteMode(ctx) != guardrails.ReadBeforeWriteOff {
		t.Fatal("shell confinement, shell policy, and read-before-write must default off")
	}
	if !s.SudoAskpass(ctx) || !s.PlanFirst(ctx) || !s.SpawnEnabled(ctx) {
		t.Fatal("sudo prompts, planning, and delegation must default on")
	}
	if mode, err := s.Svc.CredentialProtection(ctx); err != nil || mode != prefs.CredentialProtectionPassphrase {
		t.Fatalf("credential protection = %q, %v; want passphrase", mode, err)
	}
	for key, value := range map[string]string{
		prefs.KeySandbox: "on", prefs.KeyShellGuard: "strict", prefs.KeyReadBeforeWrite: "strict",
		prefs.KeySudoAskpass: "off", prefs.KeyPlanFirst: "off", prefs.KeySpawnEnabled: "off",
	} {
		if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if !s.ShellSandbox(ctx) || s.ShellGuardOff(ctx) || s.ReadBeforeWriteMode(ctx) != guardrails.ReadBeforeWriteStrict {
		t.Fatal("explicit protections were not preserved")
	}
	if s.SudoAskpass(ctx) || s.PlanFirst(ctx) || s.SpawnEnabled(ctx) {
		t.Fatal("explicit disable was not preserved")
	}
}

func TestApplicationModelTargetDefaultsAndInheritance(t *testing.T) {
	for _, tc := range []struct {
		providerKey, modelKey string
		want                  engine.ProviderSpec
	}{
		{prefs.KeyCompactProvider, prefs.KeyCompactModel, engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6-sol"}},
		{prefs.KeyJudgeProvider, prefs.KeyJudgeModel, engine.ProviderSpec{Name: "deepseek", Model: "deepseek-v4-flash"}},
		{prefs.KeySpawnDefaultExploreProvider, prefs.KeySpawnDefaultExploreModel, engine.ProviderSpec{Name: "deepseek", Model: "deepseek-v4-flash"}},
		{prefs.KeySpawnDefaultVerifyProvider, prefs.KeySpawnDefaultVerifyModel, engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6-luna"}},
		{prefs.KeySpawnDefaultImplementProvider, prefs.KeySpawnDefaultImplementModel, engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6-sol"}},
	} {
		t.Run(tc.providerKey, func(t *testing.T) {
			s := newJudgeTestSettings(t)
			ctx := t.Context()
			if got := s.ModelTarget(ctx, tc.providerKey); got != tc.want {
				t.Fatalf("default = %+v, want %+v", got, tc.want)
			}
			if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, tc.modelKey, "custom-model"); err != nil {
				t.Fatal(err)
			}
			if got := s.ModelTarget(ctx, tc.providerKey); got != (engine.ProviderSpec{Model: "custom-model"}) {
				t.Fatalf("legacy model-only override lost active provider inheritance: %+v", got)
			}
			if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, tc.providerKey, "llamacpp"); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{tc.providerKey, tc.modelKey} {
				if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, key, prefs.ActiveModelTarget); err != nil {
					t.Fatal(err)
				}
			}
			if got := s.ModelTarget(ctx, tc.providerKey); got != (engine.ProviderSpec{}) {
				t.Fatalf("explicit active reset must shadow global route: %+v", got)
			}
			for _, scope := range []prefs.Scope{prefs.ScopeWorkspace, prefs.ScopeGlobal} {
				for _, key := range []string{tc.providerKey, tc.modelKey} {
					if err := s.Svc.DeleteSetting(ctx, scope, key); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := s.ModelTarget(ctx, tc.providerKey); got != tc.want {
				t.Fatalf("cleared overrides = %+v, want built-in route %+v", got, tc.want)
			}
		})
	}
}

func TestApplicationCodexEffortAndNotifications(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()
	target := engine.ProviderSpec{Name: prefs.DefaultProvider, Model: prefs.DefaultModel}
	if got := s.ActiveProvider(ctx, target); got.Name != "openai-codex" || got.Model != "gpt-6-astra" || got.CodexEffort != "high" {
		t.Fatalf("default target = %+v", got)
	}
	if got := s.CodexEffort(ctx, target); got != "high" {
		t.Fatalf("default effort = %q, want high", got)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyCodexEffort, "(auto)"); err != nil {
		t.Fatal(err)
	}
	if got := s.CodexEffort(ctx, target); got != "" {
		t.Fatalf("explicit auto effort = %q, want provider heuristic", got)
	}
	if got := s.NotificationSounds(ctx); got != "all" {
		t.Fatalf("default notifications = %q, want all", got)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyNotificationSounds, "off"); err != nil {
		t.Fatal(err)
	}
	if got := s.NotificationSounds(ctx); got != "off" {
		t.Fatalf("explicit notifications = %q, want off", got)
	}
}
