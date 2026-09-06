package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSwitchTargetBuildFailureRetainsPersistedAndLiveTarget(t *testing.T) {
	settings := newTestSettings(t)
	oldSelection := prefs.ModelSelection{Provider: "llamacpp", Model: "old-model"}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, oldSelection)
	ui, live := newTargetUI(t, settings, oldSelection)
	before := live.RunTarget()

	cmd := ui.SwitchTarget(prefs.ModelSelection{Provider: "not-a-provider", Model: "new-model"})
	step(t, ui, cmd())

	requireSelection(t, settings, prefs.ScopeWorkspace, oldSelection)
	got := live.RunTarget()
	if got.Provider != before.Provider || got.Spec != before.Spec || got.Window != before.Window {
		t.Fatalf("live target changed after build failure: got=%+v before=%+v", got, before)
	}
	if toast := ui.ToastText(); !strings.Contains(toast, "provider switch failed") {
		t.Fatalf("toast=%q, want provider switch failure", toast)
	}
}

func TestSwitchTargetPersistenceFailureRetainsPersistedAndLiveTarget(t *testing.T) {
	base := newTestSettings(t)
	settings := engine.NewSettings(base.Store, nil, nil, "")
	oldSelection := prefs.ModelSelection{Provider: "llamacpp", Model: "old-model"}
	requireSetSelection(t, settings, prefs.ScopeGlobal, oldSelection)
	ui, live := newTargetUI(t, settings, oldSelection)
	before := live.RunTarget()

	cmd := ui.SwitchTarget(prefs.ModelSelection{Provider: "ollama", Model: "new-model"})
	step(t, ui, cmd())

	requireSelection(t, settings, prefs.ScopeGlobal, oldSelection)
	got := live.RunTarget()
	if got.Provider != before.Provider || got.Spec != before.Spec || got.Window != before.Window {
		t.Fatalf("live target changed after persistence failure: got=%+v before=%+v", got, before)
	}
	if toast := ui.ToastText(); !strings.Contains(toast, "persistence") {
		t.Fatalf("toast=%q, want persistence failure", toast)
	}
}

func TestSwitchTargetPersistsThenAppliesLiveTarget(t *testing.T) {
	settings := newTestSettings(t)
	oldSelection := prefs.ModelSelection{Provider: "llamacpp", Model: "old-model"}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, oldSelection)
	ui, live := newTargetUI(t, settings, oldSelection)
	selection := prefs.ModelSelection{Provider: "ollama", Model: "new-model"}

	cmd := ui.SwitchTarget(selection)
	step(t, ui, cmd())

	requireSelection(t, settings, prefs.ScopeWorkspace, selection)
	got := live.RunTarget()
	wantSpec := engine.ProviderSpec{Name: selection.Provider, Model: selection.Model}
	if got.Provider == nil || got.Spec != wantSpec || got.Model != selection.Model {
		t.Fatalf("live target=%+v, want spec=%+v", got, wantSpec)
	}
	if got := ui.ActiveProviderSpec(); got != wantSpec {
		t.Fatalf("session target=%+v, want=%+v", got, wantSpec)
	}
}

func TestSwitchTargetRejectsStaleCompletion(t *testing.T) {
	settings := newTestSettings(t)
	oldSelection := prefs.ModelSelection{Provider: "llamacpp", Model: "old-model"}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, oldSelection)
	ui, live := newTargetUI(t, settings, oldSelection)
	staleSelection := prefs.ModelSelection{Provider: "ollama", Model: "stale-model"}
	winningSelection := prefs.ModelSelection{Provider: "llamacpp", Model: "winning-model"}

	staleCmd := ui.SwitchTarget(staleSelection)
	winningCmd := ui.SwitchTarget(winningSelection)
	staleMsg := staleCmd()
	step(t, ui, winningCmd())
	step(t, ui, staleMsg)

	requireSelection(t, settings, prefs.ScopeWorkspace, winningSelection)
	got := live.RunTarget()
	wantSpec := engine.ProviderSpec{Name: winningSelection.Provider, Model: winningSelection.Model}
	if got.Spec != wantSpec || got.Model != winningSelection.Model {
		t.Fatalf("live target=%+v, want winning spec=%+v", got, wantSpec)
	}
}

func newTargetUI(t *testing.T, settings *engine.Settings, selection prefs.ModelSelection) (*tui.UI, *engine.LiveRunner) {
	t.Helper()
	provider, err := engine.BuildProvider(t.Context(), settings.Registry, settings.Svc, engine.ProviderSpec{
		Name:  selection.Provider,
		Model: selection.Model,
	})
	if err != nil {
		t.Fatalf("build initial provider: %v", err)
	}
	live := newQueueLive(t)
	ui := tui.New()
	ui.SetSettings(settings)
	ui.SetLiveRunner(live)
	ui.RepointProvider(provider, engine.ProviderSpec{Name: selection.Provider, Model: selection.Model}, 32_768, nil)
	return ui, live
}

func requireSetSelection(t *testing.T, settings *engine.Settings, scope prefs.Scope, selection prefs.ModelSelection) {
	t.Helper()
	if err := settings.Svc.SetModelSelection(t.Context(), scope, selection); err != nil {
		t.Fatalf("set model selection: %v", err)
	}
}

func requireSelection(t *testing.T, settings *engine.Settings, scope prefs.Scope, want prefs.ModelSelection) {
	t.Helper()
	provider, err := settings.Svc.GetSetting(t.Context(), scope, prefs.KeyProvider)
	if err != nil {
		t.Fatalf("get provider selection: %v", err)
	}
	model, err := settings.Svc.GetSetting(t.Context(), scope, prefs.KeyModel)
	if err != nil {
		t.Fatalf("get model selection: %v", err)
	}
	got := prefs.ModelSelection{Provider: provider.Value, Model: model.Value}
	if got != want {
		t.Fatalf("persisted selection=%+v, want=%+v", got, want)
	}
}
