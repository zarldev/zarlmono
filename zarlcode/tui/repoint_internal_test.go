package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

func TestMaybeRepoint_NoopWhenUnwired(t *testing.T) {
	if New().maybeRepoint() != nil {
		t.Error("maybeRepoint should be nil with no live runner / settings")
	}
}

func TestMaybeRepoint_UnchangedReturnsNilMsg(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New()
	m.SetSettings(s)
	m.SetLiveRunner(engine.NewLiveRunner(nil, ws, nil, "local"))

	fb := engine.ProviderSpec{Name: "llamacpp", Model: "local"}
	m.SetProviderContext(fb, s.ActiveProvider(ctx, fb))

	cmd := m.maybeRepoint()
	if cmd == nil {
		t.Fatal("maybeRepoint returned nil with a live runner wired")
	}
	if msg := cmd(); msg != nil {
		t.Errorf("unchanged provider should yield no re-point msg, got %T", msg)
	}
}

func TestHandleRepointMsg_AppliesSwitch(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New()
	m.SetSettings(s)
	live := engine.NewLiveRunner(nil, ws, nil, "old-model")
	m.SetLiveRunner(live)
	m.SetProviderContext(engine.ProviderSpec{Name: "llamacpp", Model: "local"}, engine.ProviderSpec{Name: "llamacpp", Model: "old-model"})

	// Build a real provider (local, no network) and apply it.
	spec := engine.ProviderSpec{Name: "llamacpp", Model: "qwen3"}
	prov, err := engine.BuildProvider(ctx, s.Registry, s.Svc, spec)
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	if !m.handleRepointMsg(providerRepointedMsg{prov: prov, spec: spec, window: 64000}) {
		t.Fatal("repoint message was not consumed")
	}

	rt := live.RunTarget()
	gotModel, gotProv, gotWin := rt.Model, rt.Provider, rt.Window
	if gotModel != "qwen3" || gotProv == nil || gotWin != 64000 {
		t.Errorf("runner not re-pointed: model=%q prov=%v window=%d", gotModel, gotProv != nil, gotWin)
	}
	if m.session.ActiveProviderSpec() != spec || m.session.Model != "qwen3" || m.session.Provider != "llamacpp" {
		t.Errorf("session not updated: spec=%+v model=%q provider=%q", m.session.ActiveProviderSpec(), m.session.Model, m.session.Provider)
	}
	// The switch notification goes to the status-bar toast, NOT the timeline.
	if !strings.Contains(m.session.Toast, "switched to") {
		t.Errorf("switch should set a status-bar toast, got %q", m.session.Toast)
	}
	if len(m.timeline.items) != 0 {
		t.Errorf("switch notification must not land in the timeline (%d items)", len(m.timeline.items))
	}
}

func TestHandleRepointMsg_RefreshesCostBasis(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New()
	m.SetSettings(s)
	m.SetLiveRunner(engine.NewLiveRunner(nil, ws, nil, "local"))
	m.session.Run.local = true // started on a local backend

	// handleRepointMsg derives the cost basis from the spec (name/model), not
	// the provider's concrete type, so a keyless-built provider under a
	// deepseek spec exercises the refresh without needing a deepseek key.
	prov, err := engine.BuildProvider(ctx, s.Registry, s.Svc, engine.ProviderSpec{Name: "llamacpp", Model: "x"})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	spec := engine.ProviderSpec{Name: "deepseek", Model: "deepseek-v4-pro"}
	m.handleRepointMsg(providerRepointedMsg{prov: prov, spec: spec, window: 64000})

	if m.session.Run.local {
		t.Error("deepseek is hosted — local flag must clear after re-point")
	}
	if m.session.Run.inCostPer1k <= 0 {
		t.Error("deepseek should have a metered rate after re-point")
	}
}

func TestApplyLimits_FlowsSettingsToRunner(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(nil, ws, nil, "m")
	m := New()
	m.SetSettings(s)
	m.SetLiveRunner(live)

	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyReserveTokens, "2048"); err != nil {
		t.Fatal(err)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyMaxIterations, "40"); err != nil {
		t.Fatal(err)
	}
	m.applyLimits()

	rt := live.RunTarget()
	gotReserve, gotIter := rt.Reserve, rt.MaxIter
	if gotReserve != 2048 || gotIter != 40 {
		t.Errorf("limits not applied: reserve=%d maxIter=%d, want 2048/40", gotReserve, gotIter)
	}
}

func TestHandleRepointMsg_ErrorIsNoticed(t *testing.T) {
	m := New()
	m.SetLiveRunner(engine.NewLiveRunner(nil, code.Workspace{}, nil, "m"))
	if !m.handleRepointMsg(providerRepointedMsg{err: errors.New("boom")}) {
		t.Fatal("error repoint message should be consumed")
	}
	// the live runner keeps its old target on failure
	gotModel := m.live.RunTarget().Model
	if gotModel != "m" {
		t.Errorf("failed switch must not change the model, got %q", gotModel)
	}
	// The failure surfaces in the status-bar toast, not the timeline.
	if !strings.Contains(m.session.Toast, "✗") {
		t.Errorf("failed switch should set an error toast, got %q", m.session.Toast)
	}
	if len(m.timeline.items) != 0 {
		t.Errorf("failure notification must not land in the timeline (%d items)", len(m.timeline.items))
	}
}
