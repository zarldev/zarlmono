package tui_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestExactSessionRejectsCustomTargetBeforePersistence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
		if err := settings.Registry.UpsertProvider(t.Context(), backends.ProviderDefinition{
			Name: "custom", DisplayName: "Custom", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
			BaseURL: "http://127.0.0.1:1/v1", DefaultModel: "custom-model", ContextWindow: 64000, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		f.ui.SetSettings(settings)
		selection := prefs.ModelSelection{Provider: "openai", Model: "saved-model"}
		requireSetSelection(t, settings, prefs.ScopeWorkspace, selection)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "exact history")
		id := f.ui.SessionIdentity()
		before, err := f.store.SessionVersion(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		target := f.live.RunTarget()
		driveBeforeCommand(f, f.ui.SwitchTarget(prefs.ModelSelection{Provider: "custom", Model: "custom-model"}))
		if !strings.Contains(f.ui.ToastText(), "start a new conversation") {
			t.Fatalf("missing immediate target restriction: %s", f.ui.ToastText())
		}
		requireSelection(t, settings, prefs.ScopeWorkspace, selection)
		after, err := f.store.SessionVersion(t.Context(), id)
		if err != nil || before != after || f.ui.SessionIdentity() != id {
			t.Fatalf("rejected switch altered the saved exact head: %v", err)
		}
		if got := f.live.RunTarget(); got.Provider != target.Provider || got.Spec != target.Spec {
			t.Fatal("rejected switch changed live route")
		}
		settleRewindTurn(t, f, "still works on original route")
	})
}

func TestOrdinaryProviderQualifiedSwitchDoesNotPromoteHistory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.provider.check = func(context.Context) {}
		f.ui.RepointProvider(f.provider, engine.ProviderSpec{Name: "custom", Model: "saved-model"}, 64000, nil)
		settleRewindTurn(t, f, "ordinary history")
		f.ui.RepointProvider(f.provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"}, 64000, nil)
		settleRewindTurn(t, f, "qualified route with ordinary history")
		checkpoints, err := f.store.ListSessionCheckpoints(t.Context(), f.ui.SessionIdentity())
		if err != nil || len(checkpoints) != 0 {
			t.Fatalf("mixed history falsely promoted to exact: %d, %v", len(checkpoints), err)
		}
		f.live.RestoreContext(nil)
		if err := f.ui.ResumeSavedSession(t.Context(), f.ui.SessionIdentity()); err != nil {
			t.Fatal(err)
		}
		if len(f.live.ContextSnapshot()) < 4 {
			t.Fatal("mixed-route context not restored")
		}
	})
}
