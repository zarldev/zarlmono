package tui_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

func TestApplyLimitsFlowsSettingsToRunner(t *testing.T) {
	s := newTestSettings(t)
	live := newQueueLive(t)
	m := tui.New()
	m.SetSettings(s)
	m.SetLiveRunner(live)
	if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyReserveTokens, "2048"); err != nil {
		t.Fatal(err)
	}
	if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyMaxIterations, "40"); err != nil {
		t.Fatal(err)
	}
	m.ApplyLimits()
	rt := live.RunTarget()
	if rt.Reserve != 2048 || rt.MaxIter != 40 {
		t.Fatalf("target=%+v", rt)
	}
}

func TestRepointProviderAppliesAtomicTargetAndSession(t *testing.T) {
	s := newTestSettings(t)
	live := newQueueLive(t)
	m := tui.New()
	m.SetSettings(s)
	m.SetLiveRunner(live)
	spec := engine.ProviderSpec{Name: "llamacpp", Model: "qwen3"}
	prov, err := engine.BuildProvider(t.Context(), s.Registry, s.Svc, spec)
	if err != nil {
		t.Fatal(err)
	}
	m.RepointProvider(prov, spec, 64000, nil)
	rt := live.RunTarget()
	if rt.Model != "qwen3" || rt.Provider == nil || rt.Window != 64000 {
		t.Fatalf("target=%+v", rt)
	}
	if got := m.ActiveProviderSpec(); got != spec {
		t.Fatalf("session spec=%+v", got)
	}
}
