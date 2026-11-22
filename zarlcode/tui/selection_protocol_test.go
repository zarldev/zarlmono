package tui_test

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestModelSelectionRetainsRuntimeProtocol(t *testing.T) {
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLRESPONSES, llm.ModelProtocols.PROTOCOLMESSAGES} {
		t.Run(protocol.String(), func(t *testing.T) {
			settings, ui, live, initial := newProtocolSelectionUI(t, protocol)
			ui.SetModelChoices(initial.Name, []string{initial.Model, "deployment/New Alias"})

			step(t, ui, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
			step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
			_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runSettingsCloseCmd(t, ui, cmd)

			want := initial
			want.Model = "deployment/New Alias"
			requireProtocolTarget(t, live, want)
			requireSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: want.Name, Model: want.Model})
			// The settings-close rebuild path must also retain the CLI override.
			closeSettings(t, ui)
			requireProtocolTarget(t, live, want)
		})
	}
}

func TestModelSelectionDifferentProviderDoesNotInheritRuntimeProtocol(t *testing.T) {
	settings, ui, live, initial := newProtocolSelectionUI(t, llm.ModelProtocols.PROTOCOLRESPONSES)
	ui.SetModelChoices(initial.Name, []string{initial.Model})
	ui.SetModelChoices("llamacpp", []string{"other-model"})

	step(t, ui, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyTab})
	_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runSettingsCloseCmd(t, ui, cmd)

	want := engine.ProviderSpec{Name: "llamacpp", Model: "other-model"}
	if got := live.RunTarget(); got.Spec != want || got.Provider.Name() != want.Name || got.Model != want.Model {
		t.Fatalf("different-provider target = %+v, want %+v", got, want)
	}
	requireSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: want.Name, Model: want.Model})
}

func TestModelSelectionBuildFailureRetainsRuntimeProtocol(t *testing.T) {
	settings, ui, live, initial := newProtocolSelectionUI(t, llm.ModelProtocols.PROTOCOLRESPONSES)
	before := live.RunTarget()
	// Cached picker choices can outlive a removed provider definition.
	ui.SetModelChoices(initial.Name, []string{initial.Model, "deployment/New Alias"})
	step(t, ui, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions(nil))
	_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runSettingsCloseCmd(t, ui, cmd)

	requireProtocolTarget(t, live, initial)
	if got := live.RunTarget(); got.Provider != before.Provider || got.Window != before.Window {
		t.Fatal("build failure replaced the live provider or context window")
	}
	requireSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: initial.Name, Model: initial.Model})
}

func TestOrdinarySessionSelectionRetainsRuntimeProtocol(t *testing.T) {
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLRESPONSES, llm.ModelProtocols.PROTOCOLMESSAGES} {
		t.Run(protocol.String(), func(t *testing.T) {
			settings, ui, live, initial := newProtocolSelectionUI(t, protocol)
			want := initial
			want.Model = "deployment/Saved Alias"
			selectOrdinaryProtocolSession(t, settings, ui, want.Name, want.Model)
			requireProtocolTarget(t, live, want)
			requireSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: want.Name, Model: want.Model})
		})
	}
}

func TestOrdinarySessionDifferentProviderDoesNotInheritRuntimeProtocol(t *testing.T) {
	settings, ui, live, _ := newProtocolSelectionUI(t, llm.ModelProtocols.PROTOCOLRESPONSES)
	want := engine.ProviderSpec{Name: "llamacpp", Model: "saved-local-model"}
	selectOrdinaryProtocolSession(t, settings, ui, want.Name, want.Model)
	if got := live.RunTarget(); got.Spec != want || got.Provider.Name() != want.Name || got.Model != want.Model {
		t.Fatalf("different-provider restored target = %+v, want %+v", got, want)
	}
	requireSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: want.Name, Model: want.Model})
}

func newProtocolSelectionUI(t *testing.T, protocol llm.ModelProtocol) (*engine.Settings, *tui.UI, *engine.LiveRunner, engine.ProviderSpec) {
	t.Helper()
	settings := newTestSettings(t)
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{
		{Name: "litellm", AdapterType: backends.AdapterTypes.LITELLM, BaseURL: "http://127.0.0.1:1/registry", ContextWindow: 32768, Enabled: true, Builtin: true},
		{Name: "llamacpp", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "http://127.0.0.1:1/local", ContextWindow: 32768, Enabled: true},
	}))
	initial := engine.ProviderSpec{
		Name: "litellm", Model: "deployment/Original Alias", Protocol: protocol,
		BaseURL: "http://127.0.0.1:1/cli-gateway", APIKey: "fixture-key",
	}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: initial.Name, Model: initial.Model})
	provider, err := engine.BuildProvider(t.Context(), settings.Registry, settings.Svc, initial)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	live := engine.NewLiveRunner(provider, ws, initial.Model)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	live.SetProviderSpec(provider, initial)
	ui := tui.New()
	ui.SetSettings(settings)
	ui.SetLiveRunner(live)
	ui.SetProviderContext(initial, initial)
	return settings, ui, live, initial
}

func requireProtocolTarget(t *testing.T, live *engine.LiveRunner, want engine.ProviderSpec) {
	t.Helper()
	got := live.RunTarget()
	// Check the real built adapter, not just the spec carried alongside it.
	provider, ok := got.Provider.(*litellm.Provider)
	if !ok {
		t.Fatalf("built provider = %T, want LiteLLM", got.Provider)
	}
	if provider.Protocol() != want.Protocol {
		t.Fatalf("built protocol = %s, want %s", provider.Protocol(), want.Protocol)
	}
	if got.Spec != want || got.Model != want.Model {
		t.Fatalf("runtime target = %+v, want %+v", got.Spec, want)
	}
}

func selectOrdinaryProtocolSession(t *testing.T, settings *engine.Settings, ui *tui.UI, provider, model string) {
	t.Helper()
	if err := settings.Store.SaveSessionDraft(t.Context(), db.SessionRecord{
		ID: "ordinary-protocol-session", Workspace: settings.WorkspaceRoot(),
		Provider: provider, Model: model, PendingJSON: []byte(`{"text":"saved draft"}`),
	}); err != nil {
		t.Fatal(err)
	}
	ui.ActivateIntro(t.Context())
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyTab})   // focus saved sessions
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter}) // open target choice
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter}) // use saved target
}
