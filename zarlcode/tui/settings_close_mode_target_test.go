package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSettingsCloseRetainsCurrentModeRouteWithoutTargetEdits(t *testing.T) {
	settings := newTestSettings(t)
	primary := prefs.ModelSelection{Provider: "llamacpp", Model: "primary-model"}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, primary)
	setPlanRoute(t, settings, "ollama", "plan-model")
	ui, live := newTargetUI(t, settings, primary)
	step(t, ui, schedulePlanTarget(t, ui)())

	closeSettings(t, ui)

	want := engine.ProviderSpec{Name: "ollama", Model: "plan-model"}
	if got := live.RunTarget().Spec; got != want {
		t.Fatalf("unchanged settings close replaced routed target: got=%+v want=%+v", got, want)
	}
	requireSelection(t, settings, prefs.ScopeWorkspace, primary)
}

func TestSettingsPrimaryEditOverridesCurrentModeRoute(t *testing.T) {
	settings := newTestSettings(t)
	primary := prefs.ModelSelection{Provider: "llamacpp", Model: "primary-model"}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, primary)
	setPlanRoute(t, settings, "ollama", "plan-model")
	ui, live := newTargetUI(t, settings, primary)
	step(t, ui, schedulePlanTarget(t, ui)())

	openSettings(t, ui)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter}) // focus model rows
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter}) // open primary provider picker
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	_, switchCmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runSettingsCloseCmd(t, ui, switchCmd)
	closeOpenSettings(t, ui)

	selection := settings.ActiveProvider(t.Context(), engine.ProviderSpec{Name: primary.Provider, Model: primary.Model})
	if got := live.RunTarget().Spec; got != selection {
		t.Fatalf("primary settings edit did not override route: got=%+v want=%+v", got, selection)
	}
	// Closing settings again is not a mode transition and must not restore the route.
	closeSettings(t, ui)
	if got := live.RunTarget().Spec; got != selection {
		t.Fatalf("manual override did not survive unchanged close: got=%+v want=%+v", got, selection)
	}
}

func TestCurrentModeRouteEditAppliesDespiteManualOverride(t *testing.T) {
	settings := newTestSettings(t)
	primary := prefs.ModelSelection{Provider: "llamacpp", Model: "primary-model"}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, primary)
	ui, live := newTargetUI(t, settings, primary)
	ui.SetPlanMode(true)
	if manual := ui.SwitchTarget(prefs.ModelSelection{Provider: "llamacpp", Model: "manual-model"}); manual != nil {
		step(t, ui, manual())
	}

	openSettings(t, ui)
	setPlanRoute(t, settings, "ollama", "edited-plan-model")
	closeOpenSettings(t, ui)

	want := settings.MainModeTarget(t.Context(), true, engine.ProviderSpec{Name: "llamacpp", Model: "manual-model"})
	if got := live.RunTarget().Spec; got != want {
		t.Fatalf("edited current-mode route was suppressed: got=%+v want=%+v", got, want)
	}
}

func openSettings(t *testing.T, ui *tui.UI) {
	t.Helper()
	step(t, ui, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
}

func closeSettings(t *testing.T, ui *tui.UI) {
	t.Helper()
	openSettings(t, ui)
	closeOpenSettings(t, ui)
}

func closeOpenSettings(t *testing.T, ui *tui.UI) {
	t.Helper()
	_, cmd := ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runSettingsCloseCmd(t, ui, cmd)
}

func runSettingsCloseCmd(t *testing.T, ui *tui.UI, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nested := range batch {
			runSettingsCloseCmd(t, ui, nested)
		}
		return
	}
	if msg == nil {
		return
	}
	_, next := ui.Update(msg)
	runSettingsCloseCmd(t, ui, next)
}
