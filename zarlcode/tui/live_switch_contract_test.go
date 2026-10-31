package tui_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestProviderOnlySwitchDoesNotBorrowOtherGlobalModel(t *testing.T) {
	settings := newTestSettings(t)
	requireSetSelection(t, settings, prefs.ScopeGlobal, prefs.ModelSelection{Provider: "openai", Model: "gpt-foreign"})
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "local", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "http://127.0.0.1:1", DefaultModel: "local-default", Enabled: true}}))
	ui, live := newTargetUI(t, settings, prefs.ModelSelection{Provider: "local", Model: "prior"})
	step(t, ui, ui.SwitchTarget(prefs.ModelSelection{Provider: "local"})())
	if got := live.RunTarget().Model; got != "local-default" {
		t.Fatalf("model=%s", got)
	}
	if got := live.RunTarget().Window; got != engine.LiveContextWindow {
		t.Fatalf("unknown budget=%d", got)
	}
	resolved := settings.ActiveProvider(t.Context(), engine.ProviderSpec{Name: prefs.DefaultProvider, Model: prefs.DefaultModel})
	if got := live.RunTarget().Spec; got != resolved {
		t.Fatalf("live target=%+v, restart target=%+v", got, resolved)
	}
}

func TestAutomaticModeTargetNormalizesUnknownWindowForEngineAndUI(t *testing.T) {
	settings := newTestSettings(t)
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{
		{Name: "local", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "http://127.0.0.1:1", DefaultModel: "local-model", ContextWindow: 64_000, Enabled: true},
		{Name: "unknown-window", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "http://127.0.0.1:1", DefaultModel: "route-model", Enabled: true},
	}))
	setPlanRoute(t, settings, "unknown-window", "route-model")
	ui, live := newTargetUI(t, settings, prefs.ModelSelection{Provider: "local", Model: "local-model"})
	live.SetContextWindow(64_000)
	ui.SetContextWindow(64_000)
	ui.SetPressureConfig(64_000, 4_000)

	step(t, ui, schedulePlanTarget(t, ui)())
	if got := live.RunTarget().Window; got != engine.LiveContextWindow {
		t.Fatalf("engine window=%d, want %d", got, engine.LiveContextWindow)
	}
	cockpit := ansi.Strip(strings.Join(ui.RenderCockpitLines(80), "\n"))
	if !strings.Contains(cockpit, "32.8k") || strings.Contains(cockpit, "64.0k") {
		t.Fatalf("UI pressure kept stale window:\n%s", cockpit)
	}
}

func TestRejectedManualSwitchPreservesAutomaticModeTarget(t *testing.T) {
	settings := newTestSettings(t)
	setPlanRoute(t, settings, "ollama", "automatic-plan")
	ui, live := newTargetUI(t, settings, prefs.ModelSelection{Provider: "llamacpp", Model: "prior"})
	pending := ui.SwitchTarget(prefs.ModelSelection{Provider: "ollama", Model: "rejected-manual"})
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	step(t, ui, pending())
	reservation.Release()
	if !strings.Contains(ui.ToastText(), "deferred") {
		t.Fatalf("not rejected: %s", ui.ToastText())
	}
	step(t, ui, schedulePlanTarget(t, ui)())
	if got := live.RunTarget().Model; got != "automatic-plan" {
		t.Fatalf("failed manual switch disabled mode routing: %s", got)
	}
}

func TestLiveSwitchRejectsUnsupportedHistoryAndPreservesPreferences(t *testing.T) {
	settings := newTestSettings(t)
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "openai", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "http://127.0.0.1:1", DefaultModel: "gpt-5.4", Enabled: true}, {Name: "gemini", AdapterType: backends.AdapterTypes.GOOGLECOMPATIBLE, BaseURL: "http://127.0.0.1:1", DefaultModel: "gemini-2.5-flash", Enabled: true}}), backends.WithSettingsService(sessionFixtureKeys{}))
	ui, live := newTargetUI(t, settings, prefs.ModelSelection{Provider: "openai", Model: "gpt-5.4"})
	initial := prefs.ModelSelection{Provider: "openai", Model: "gpt-5.4"}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, initial)
	history := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: "https://private.invalid/image"}}}}}
	live.RestoreContext(history)
	step(t, ui, ui.SwitchTarget(prefs.ModelSelection{Provider: "gemini", Model: "gemini-2.5-flash"})())
	requireSelection(t, settings, prefs.ScopeWorkspace, initial)
	if live.RunTarget().Spec.Name != "openai" || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("rejection changed active state")
	}
	if !strings.Contains(ui.ToastText(), "incompatible") {
		t.Fatalf("toast=%s", ui.ToastText())
	}
}
