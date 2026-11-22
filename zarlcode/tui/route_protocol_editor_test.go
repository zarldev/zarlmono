package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

func newRouteEditorUI(t *testing.T, protocol llm.ModelProtocol) (*engine.Settings, *tui.UI, *engine.LiveRunner, engine.ProviderSpec) {
	t.Helper()
	s, ui, live, spec := newProtocolSelectionUI(t, protocol)
	s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(routeEditorDefinitions()), backends.WithRouteProtocolStore(s.Store))
	// Establish the applied definition baseline through the real public switch.
	step(t, ui, routeManualBuild(t, ui, spec.Name, spec.Model)())
	step(t, ui, window(170, 50))
	return s, ui, live, spec
}

func routeEditorDefinitions() []backends.ProviderDefinition {
	return []backends.ProviderDefinition{
		{Name: "litellm", AdapterType: backends.AdapterTypes.LITELLM, BaseURL: "http://127.0.0.1:1/registry", DefaultModel: "fallback/alias", ContextWindow: 32768, Enabled: true, Builtin: true},
		{Name: "proxy Δ", AdapterType: backends.AdapterTypes.LITELLM, BaseURL: "http://127.0.0.1:1/custom", DefaultModel: "custom/default", ContextWindow: 32768, Enabled: true},
	}
}

func openRouteEditor(t *testing.T, ui *tui.UI, custom bool) {
	t.Helper()
	openSettings(t, ui)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	if custom {
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	requireRouteView(t, ui, "r route protocol")
	step(t, ui, textKey("r"))
	requireRouteView(t, ui, "Connection policy · shared across workspaces")
}

func routeManualBuild(t *testing.T, ui *tui.UI, provider, model string) tea.Cmd {
	t.Helper()
	ui.SetModelChoices(provider, []string{model})
	step(t, ui, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("model picker did not schedule target build")
	}
	return cmd
}

func routeToggleMode(t *testing.T, ui *tui.UI) tea.Cmd {
	t.Helper()
	_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	return cmd
}

func selectRouteChoice(t *testing.T, ui *tui.UI, from, to int) {
	t.Helper()
	for range (to - from + 5) % 5 {
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	}
}

func saveRouteChoice(t *testing.T, ui *tui.UI, from, to int) {
	t.Helper()
	selectRouteChoice(t, ui, from, to)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func closeRouteEditor(t *testing.T, ui *tui.UI) {
	t.Helper()
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEscape})
	closeOpenSettings(t, ui)
}

func requireRouteView(t *testing.T, ui *tui.UI, text string) {
	t.Helper()
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, text) {
		t.Fatalf("view missing %q:\n%s", text, view)
	}
}

func requireRoutePolicy(t *testing.T, s *engine.Settings, provider, alias string, want llm.ModelProtocol) {
	t.Helper()
	got, err := s.Registry.GetRouteProtocol(t.Context(), provider, alias)
	if want == llm.ModelProtocols.PROTOCOLUNKNOWN {
		if !errors.Is(err, backends.ErrRouteProtocolNotFound) {
			t.Fatalf("policy %q/%q = %s, %v; want absent", provider, alias, got, err)
		}
		return
	}
	if err != nil || got != want {
		t.Fatalf("policy %q/%q = %s, %v; want %s", provider, alias, got, err, want)
	}
}

func requireRouteRuntime(t *testing.T, live *engine.LiveRunner, spec engine.ProviderSpec, protocol llm.ModelProtocol) {
	t.Helper()
	target := live.RunTarget()
	provider, ok := target.Provider.(*litellm.Provider)
	if !ok || provider.Protocol() != protocol || target.Spec != spec {
		t.Fatalf("runtime = %+v (%T); want spec=%+v protocol=%s", target, target.Provider, spec, protocol)
	}
}

func TestRouteProtocolEditorFourProtocolsClearAndReopen(t *testing.T) {
	s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	choices := []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLRESPONSES, llm.ModelProtocols.PROTOCOLMESSAGES, llm.ModelProtocols.PROTOCOLGENERATECONTENT, llm.ModelProtocols.PROTOCOLUNKNOWN}
	from := 0
	for i, want := range choices {
		openRouteEditor(t, ui, false)
		requireRouteView(t, ui, `Alias: "deployment/Original Alias"`)
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
		to := (i + 1) % 5
		before := live.RunTarget().Provider
		saveRouteChoice(t, ui, from, to)
		requireRoutePolicy(t, s, spec.Name, spec.Model, want)
		if live.RunTarget().Provider != before {
			t.Fatal("durable save changed runtime before settings close")
		}
		closeRouteEditor(t, ui)
		effective := want
		if i == 4 {
			effective = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
		}
		requireRouteRuntime(t, live, spec, effective)
		if i == 0 && live.RunTarget().Provider != before {
			t.Fatal("explicit Chat rebuilt the default Chat provider")
		}
		if i != 0 && live.RunTarget().Provider == before {
			t.Fatal("changed effective protocol did not refresh")
		}
		requireSelection(t, s, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: spec.Name, Model: spec.Model})
		openRouteEditor(t, ui, false)
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
		if to == 0 {
			requireRouteView(t, ui, "Not configured; default Chat Completions.")
		} else {
			requireRouteView(t, ui, "Saved policy: "+want.String())
		}
		if to == 0 {
			requireRouteView(t, ui, "[not configured]")
		} else {
			requireRouteView(t, ui, "[saved]")
		}
		closeRouteEditor(t, ui)
		from = to
	}
}

func TestRouteProtocolEditorExactAliasPasteAndIsolation(t *testing.T) {
	s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	before := live.RunTarget().Provider
	alias := "  Team/Δ/Case\r\n\x1b[31m\t  "
	openRouteEditor(t, ui, true)
	requireRouteView(t, ui, `Provider: "proxy Δ"`)
	requireRouteView(t, ui, `Alias: "custom/default"`)
	step(t, ui, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	step(t, ui, tea.PasteMsg{Content: alias})
	for _, text := range []string{"q", "j", "k"} {
		step(t, ui, textKey(text))
	}
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyLeft})
	step(t, ui, textKey("Z"))
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyRight})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyTab})
	wantAlias := alias + "qjZk"
	requireRouteView(t, ui, `\r\n\x1b[31m\t`)
	saveRouteChoice(t, ui, 0, 3)
	requireRoutePolicy(t, s, "proxy Δ", wantAlias, llm.ModelProtocols.PROTOCOLMESSAGES)
	for _, pair := range [][2]string{{"proxy Δ", strings.TrimSpace(wantAlias)}, {"proxy Δ", strings.ToLower(wantAlias)}, {"litellm", wantAlias}, {spec.Name, spec.Model}} {
		requireRoutePolicy(t, s, pair[0], pair[1], llm.ModelProtocols.PROTOCOLUNKNOWN)
	}
	closeRouteEditor(t, ui)
	if live.RunTarget().Provider != before {
		t.Fatal("unrelated exact pair replaced active provider")
	}
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS)
}

func TestRouteProtocolEditorSelectionCancelAndEditRevert(t *testing.T) {
	s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	before := live.RunTarget().Provider
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	selectRouteChoice(t, ui, 0, 2)
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLUNKNOWN)
	closeRouteEditor(t, ui)
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	saveRouteChoice(t, ui, 0, 2)
	saveRouteChoice(t, ui, 2, 1)
	closeRouteEditor(t, ui)
	if live.RunTarget().Provider != before {
		t.Fatal("effective edit then revert rebuilt runtime")
	}
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS)
}

func TestRouteProtocolEditorRuntimeOverrideWins(t *testing.T) {
	s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLRESPONSES)
	before := live.RunTarget().Provider
	openRouteEditor(t, ui, false)
	requireRouteView(t, ui, "Runtime CLI protocol override wins for this run: responses")
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	saveRouteChoice(t, ui, 0, 3)
	closeRouteEditor(t, ui)
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLRESPONSES)
	if live.RunTarget().Provider != before {
		t.Fatal("masked policy rebuilt runtime")
	}
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLMESSAGES)
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	saveRouteChoice(t, ui, 3, 0)
	closeRouteEditor(t, ui)
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLUNKNOWN)
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLRESPONSES)
	requireSelection(t, s, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: spec.Name, Model: spec.Model})
}

func TestRouteProtocolEditorLongUnicodeAliasIsScrollable(t *testing.T) {
	s, ui, _, _ := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	step(t, ui, window(70, 14))
	openRouteEditor(t, ui, false)
	alias := strings.Repeat("漢字/", 100) + " EXACT_END "
	step(t, ui, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	step(t, ui, tea.PasteMsg{Content: alias})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	var seen strings.Builder
	for range 40 {
		seen.WriteString(ansi.Strip(ui.View().Content))
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if !strings.Contains(seen.String(), "EXACT_END") || !strings.Contains(seen.String(), "Responses") {
		t.Fatalf("long alias or choices inaccessible by scrolling:\n%s", seen.String())
	}
	// Keyboard choice/save still uses the exact, unwrapped alias.
	saveRouteChoice(t, ui, 0, 2)
	requireRoutePolicy(t, s, "litellm", alias, llm.ModelProtocols.PROTOCOLRESPONSES)
	closeRouteEditor(t, ui)
}
