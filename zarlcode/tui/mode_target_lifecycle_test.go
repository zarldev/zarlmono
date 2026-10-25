package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestModeTargetTransitionAppliesToNextTarget(t *testing.T) {
	settings := newTestSettings(t)
	setPlanRoute(t, settings, "ollama", "plan-model")
	ui, live := newTargetUI(t, settings, prefs.ModelSelection{Provider: "llamacpp", Model: "build-model"})

	cmd := schedulePlanTarget(t, ui)
	step(t, ui, cmd())

	want := engine.ProviderSpec{Name: "ollama", Model: "plan-model"}
	if got := live.RunTarget(); got.Spec != want || got.Model != want.Model {
		t.Fatalf("live target=%+v, want=%+v", got, want)
	}
	if got := ui.ActiveProviderSpec(); got != want {
		t.Fatalf("published UI target=%+v, want=%+v", got, want)
	}
}

func TestManualTargetOverrideRejectsStaleModeCompletion(t *testing.T) {
	settings := newTestSettings(t)
	setPlanRoute(t, settings, "ollama", "stale-plan")
	ui, live := newTargetUI(t, settings, prefs.ModelSelection{Provider: "llamacpp", Model: "build-model"})

	stale := schedulePlanTarget(t, ui)()
	manual := prefs.ModelSelection{Provider: "llamacpp", Model: "manual-model"}
	step(t, ui, ui.SwitchTarget(manual)())
	step(t, ui, stale)

	want := engine.ProviderSpec{Name: manual.Provider, Model: manual.Model}
	if got := live.RunTarget(); got.Spec != want || got.Model != want.Model {
		t.Fatalf("stale mode completion replaced manual target: got=%+v want=%+v", got, want)
	}
}

func TestModeTargetErrorRetainsTargetUntilManualRecovery(t *testing.T) {
	settings := newTestSettings(t)
	setPlanRoute(t, settings, "not-a-provider", "broken")
	initial := prefs.ModelSelection{Provider: "llamacpp", Model: "build-model"}
	ui, live := newTargetUI(t, settings, initial)

	step(t, ui, schedulePlanTarget(t, ui)())
	step(t, ui, window(140, 40))
	if view := ansi.Strip(ui.View().Content); !strings.Contains(view, "prepare Plan target") {
		t.Fatalf("mode target error is not visible:\n%s", view)
	}
	if got := live.RunTarget(); got.Spec.Name != initial.Provider || got.Model != initial.Model {
		t.Fatalf("failed route changed target: %+v", got)
	}

	manual := prefs.ModelSelection{Provider: "llamacpp", Model: "recovered"}
	step(t, ui, ui.SwitchTarget(manual)())
	if got := live.RunTarget(); got.Spec.Model != manual.Model {
		t.Fatalf("manual recovery target=%+v", got)
	}
}

func TestModeTargetBusyCompletionWaitsForSubmitReservation(t *testing.T) {
	settings := newTestSettings(t)
	setPlanRoute(t, settings, "ollama", "plan-model")
	initial := prefs.ModelSelection{Provider: "llamacpp", Model: "build-model"}
	ui, live := newTargetUI(t, settings, initial)
	ui.SetStartupReady(true)

	cmd := schedulePlanTarget(t, ui)
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatalf("reserve runtime: %v", err)
	}
	step(t, ui, cmd())
	if got := live.RunTarget(); got.Spec.Name != initial.Provider || got.Model != initial.Model {
		t.Fatalf("busy route falsely published live target: %+v", got)
	}
	if got := ui.ActiveProviderSpec(); got.Name != initial.Provider || got.Model != initial.Model {
		t.Fatalf("busy route falsely published UI target: %+v", got)
	}

	reservation.Release()
	if cmd := ui.Submit("use prepared plan target"); cmd == nil {
		t.Fatal("submit did not reserve the prepared target turn")
	}
	want := engine.ProviderSpec{Name: "ollama", Model: "plan-model"}
	if got := live.RunTarget(); got.Spec != want || got.Model != want.Model {
		t.Fatalf("submit did not publish prepared live target: got=%+v want=%+v", got, want)
	}
	if got := ui.ActiveProviderSpec(); got != want {
		t.Fatalf("submit did not publish prepared UI target: got=%+v want=%+v", got, want)
	}
}

func schedulePlanTarget(t *testing.T, ui *tui.UI) tea.Cmd {
	t.Helper()
	ui.SetPlanMode(true)
	_, cmd := ui.Update(nil)
	if cmd == nil {
		t.Fatal("mode transition did not schedule target preparation")
	}
	return cmd
}

func setPlanRoute(t *testing.T, settings *engine.Settings, provider, model string) {
	t.Helper()
	providerKey, modelKey := prefs.KeyMainPlanProvider, prefs.KeyMainPlanModel
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, providerKey, provider); err != nil {
		t.Fatal(err)
	}
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, modelKey, model); err != nil {
		t.Fatal(err)
	}
}
