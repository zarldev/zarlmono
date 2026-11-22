package tui_test

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAnthropicEmbedderRewindDropsUnprovenConstructionFacts(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "anthropic", Model: "saved-model"})
	f.ui.SetLiveRunner(f.live)
	settleRewindTurn(t, f, "saved prompt")
	savedID := f.ui.SessionIdentity()
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	f.ui.SetSettings(settings)
	target := f.live.RunTarget()
	f.live.ApplyTarget(engine.TargetUpdate{Provider: target.Provider, Spec: target.Spec, Window: 65536,
		Facts: &engine.TargetFacts{RequestedAlias: llm.ModelID(target.Model), Budget: engine.OperationalBudget{Tokens: 65536}}})
	previewRewindPrompt(f, 1)
	_, command := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	driveBeforeCommand(f, command)
	if f.ui.SessionIdentity() == savedID {
		t.Fatalf("rewind did not create a branch: %s", f.ui.ToastText())
	}
	if got := f.live.RunTarget(); got.Facts != nil || got.RateCard != nil {
		t.Fatal("rewind retained stale budget or other unproven facts without cache evidence")
	}
}
