package tui_test

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestOnboardingBlocksPromptsAndOpensSettings(t *testing.T) {
	m := tui.New()
	m.SetWorkspace(t.TempDir(), "")
	m.SetSettings(&engine.Settings{})
	m.SetOnboarding("provider is not configured", true)

	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m = model.(*tui.UI)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"setup required", "ctrl+s configure", "provider is not configured"} {
		if !strings.Contains(view, want) {
			t.Fatalf("onboarding view missing %q:\n%s", want, view)
		}
	}

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = model.(*tui.UI)
	if cmd != nil {
		t.Fatal("onboarding Enter started a command")
	}
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "configure a usable provider before starting") {
		t.Fatalf("onboarding did not explain blocked prompt:\n%s", view)
	}

	model, _ = m.Update(tea.KeyPressMsg{Code: 's', Text: "s", Mod: tea.ModCtrl})
	m = model.(*tui.UI)
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(strings.ToLower(view), "settings") {
		t.Fatalf("ctrl+s did not open settings from onboarding:\n%s", view)
	}
}

func TestOnboardingAcceptsLocalDefaultsWithoutModelRun(t *testing.T) {
	m := tui.New()
	m.SetWorkspace(t.TempDir(), "")
	modelRan := false
	m.SetRunFn(func(string) tea.Cmd {
		modelRan = true
		return nil
	})
	root := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, root)
	m.SetSettings(settings)
	spec := engine.ProviderSpec{Name: "llamacpp", Model: "local"}
	m.SetProviderContext(spec, spec)
	m.SetOnboarding("using local defaults", false)

	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m = model.(*tui.UI)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "accept defaults") {
		t.Fatalf("onboarding did not offer local defaults:\n%s", view)
	}

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = model.(*tui.UI)
	if cmd != nil || modelRan {
		t.Fatal("accepting local defaults started a model run")
	}
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "accept defaults") {
		t.Fatalf("accepting local defaults did not dismiss onboarding:\n%s", view)
	}
	provider, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyProvider)
	if err != nil || provider.Value != "llamacpp" {
		t.Fatalf("persisted provider = %#v, %v", provider, err)
	}
}
