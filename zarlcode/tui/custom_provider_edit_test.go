package tui_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/vault"
)

func customEditDefinition() backends.ProviderDefinition {
	return backends.ProviderDefinition{
		Name: "litellm", DisplayName: "Team gateway Δ", AdapterType: backends.AdapterTypes.LITELLM,
		BaseURL: "http://127.0.0.1:1/gateway", DefaultModel: "  Team/Δ/Exact Alias  ",
		SeedModels: []string{"  seed/Δ  ", "seed/Other"}, ReasoningHistory: llm.ReasoningHistories.FIELD,
		ContextWindow: 32768, InputCostPerMTok: 0.5, OutputCostPerMTok: 2.5, Enabled: true,
	}
}

func newCustomEditUI(t *testing.T, def backends.ProviderDefinition) (*engine.Settings, *tui.UI, *engine.LiveRunner) {
	t.Helper()
	s, ui, live, _ := newProtocolSelectionUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(nil), backends.WithStore(s.Store), backends.WithRouteProtocolStore(s.Store))
	if err := s.Registry.UpsertProvider(t.Context(), def); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(t.TempDir(), func(bool, bool) (string, error) { return "custom-edit-test-passphrase", nil })
	if err != nil {
		t.Fatal(err)
	}
	s.Svc.SetVault(v)
	if err := s.Svc.SetKey(t.Context(), prefs.ScopeGlobal, def.Name, "custom-edit-fake-key"); err != nil {
		t.Fatal(err)
	}
	step(t, ui, window(190, 55))
	openSettings(t, ui)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	step(t, ui, textKey("e"))
	return s, ui, live
}

func customEditTabs(t *testing.T, ui *tui.UI, n int) {
	t.Helper()
	for range n {
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyTab})
	}
}

func customEditReplace(t *testing.T, ui *tui.UI, old, value string) {
	t.Helper()
	for range []rune(old) {
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	step(t, ui, tea.PasteMsg{Content: value})
}

func customEditSave(t *testing.T, ui *tui.UI) {
	t.Helper()
	step(t, ui, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
}

func requireCustomEditDefinition(t *testing.T, s *engine.Settings, want backends.ProviderDefinition) {
	t.Helper()
	if err := s.Registry.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := s.Registry.Parse(want.Name)
	if want.Enabled && err != nil || !want.Enabled && !errors.Is(err, backends.ErrProviderDisabled) {
		t.Fatalf("reload definition: %v", err)
	}
	if diff := cmp.Diff(want, got, cmpopts.EquateComparable(backends.AdapterType{}, llm.ReasoningHistory{})); diff != "" {
		t.Fatalf("provider definition (-want +got):\n%s", diff)
	}
}

func requireCustomEditKey(t *testing.T, s *engine.Settings, name string) {
	t.Helper()
	key, err := s.Svc.GetKey(t.Context(), prefs.ScopeGlobal, name)
	if err != nil || key != "custom-edit-fake-key" {
		t.Fatalf("original credential changed: %v", err)
	}
}

func TestCustomProviderEditLiteLLMExactAliasPolicy(t *testing.T) {
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLRESPONSES, llm.ModelProtocols.PROTOCOLMESSAGES} {
		t.Run(protocol.String(), func(t *testing.T) {
			def := customEditDefinition()
			s, ui, live := newCustomEditUI(t, def)
			before := live.RunTarget()
			if err := s.Registry.SetRouteProtocol(t.Context(), def.Name, def.DefaultModel, protocol); err != nil {
				t.Fatal(err)
			}
			customEditTabs(t, ui, 4)
			customEditReplace(t, ui, "32768", "65536")
			customEditTabs(t, ui, 1)
			customEditReplace(t, ui, "0.5/2.5", "1.25/4.75")
			customEditSave(t, ui)
			def.ContextWindow, def.InputCostPerMTok, def.OutputCostPerMTok = 65536, 1.25, 4.75
			requireCustomEditDefinition(t, s, def)
			requireRoutePolicy(t, s, def.Name, def.DefaultModel, protocol)
			requireRoutePolicy(t, s, def.Name, strings.TrimSpace(def.DefaultModel), llm.ModelProtocols.PROTOCOLUNKNOWN)
			requireCustomEditKey(t, s, def.Name)
			key, err := s.Svc.GetKey(t.Context(), prefs.ScopeEffective, def.Name)
			if err != nil {
				t.Fatal(err)
			}
			built, err := s.Registry.BuildWithConfig(t.Context(), def.Name, backends.BuildConfig{APIKey: key})
			if err != nil {
				t.Fatal(err)
			}
			named, ok := built.(interface{ Unwrap() llm.Provider })
			if !ok || built.Name() != def.Name {
				t.Fatalf("custom provider = %T, name=%q", built, built.Name())
			}
			codec, ok := named.Unwrap().(*litellm.Provider)
			if !ok || codec.Protocol() != protocol {
				t.Fatalf("built codec = %T; want LiteLLM %s", built, protocol)
			}
			after := live.RunTarget()
			if after.Provider != before.Provider || after.Spec != before.Spec || after.Window != before.Window {
				t.Fatal("definition-only edit rebuilt or changed the active target")
			}
			requireSelection(t, s, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: before.Spec.Name, Model: before.Spec.Model})
		})
	}
}

func TestCustomProviderEditHeadingAndSeedSnapshot(t *testing.T) {
	def := customEditDefinition()
	s, ui, _ := newCustomEditUI(t, def)
	requireRouteView(t, ui, "edit custom provider (litellm)")
	// All exposes the registry's seed backing slice. The already-open form
	// must own its snapshot even when a registry consumer modifies that slice.
	all := s.Registry.All()
	all[0].SeedModels[0] = "changed outside form"
	customEditSave(t, ui)
	requireCustomEditDefinition(t, s, def)
}

func TestCustomProviderEditDisabledAndOrdinaryOpenAI(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			def := customEditDefinition()
			def.AdapterType = backends.AdapterTypes.OPENAICOMPATIBLE
			def.Enabled = enabled
			s, ui, _ := newCustomEditUI(t, def)
			requireRouteView(t, ui, "edit custom provider ("+backends.AdapterTypes.OPENAICOMPATIBLE.String()+")")
			customEditTabs(t, ui, 2)
			customEditReplace(t, ui, def.DefaultModel, "  replacement/漢字  ")
			customEditSave(t, ui)
			def.DefaultModel = "  replacement/漢字  "
			requireCustomEditDefinition(t, s, def)
		})
	}
}

func TestCustomProviderEditCancelAndSuccessResetBeforeCreate(t *testing.T) {
	for _, exit := range []string{"cancel", "success", "early-close"} {
		t.Run(exit, func(t *testing.T) {
			def := customEditDefinition()
			def.Enabled = false
			s, ui, _ := newCustomEditUI(t, def)
			switch exit {
			case "success":
				customEditSave(t, ui)
			case "early-close":
				registry := s.Registry
				s.Registry = nil
				customEditSave(t, ui)
				s.Registry = registry
			case "cancel":
				customEditTabs(t, ui, 2)
				customEditReplace(t, ui, def.DefaultModel, "unsaved")
				step(t, ui, tea.KeyPressMsg{Code: tea.KeyEscape})
			}
			step(t, ui, textKey("n"))
			requireRouteView(t, ui, "add custom provider (openai-compatible)")
			for _, value := range []string{" created-proxy ", " http://127.0.0.1:1/v1 ", "  new/Δ alias  ", "unrecognised", "-42", "-1/3.5"} {
				step(t, ui, tea.PasteMsg{Content: value})
				customEditTabs(t, ui, 1)
			}
			customEditSave(t, ui)
			requireCustomEditDefinition(t, s, backends.ProviderDefinition{
				Name: "created-proxy", DisplayName: "created-proxy", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
				BaseURL: "http://127.0.0.1:1/v1", DefaultModel: "new/Δ alias", ReasoningHistory: llm.ReasoningHistories.INLINE,
				Enabled: true, OutputCostPerMTok: 3.5, SeedModels: []string{},
			})
			requireCustomEditDefinition(t, s, def)
		})
	}
}

func TestCustomProviderEditFailedSaveRetainsSnapshot(t *testing.T) {
	def := customEditDefinition()
	s, ui, _ := newCustomEditUI(t, def)
	customEditReplace(t, ui, def.Name, "invalid name")
	customEditSave(t, ui)
	requireRouteView(t, ui, "edit custom provider (litellm)")
	requireCustomEditDefinition(t, s, def)
	customEditReplace(t, ui, "invalid name", def.Name)
	customEditSave(t, ui)
	requireCustomEditDefinition(t, s, def)
}

func TestCustomProviderEditRenameWarnsWithoutKeyOrPolicyMigration(t *testing.T) {
	def := customEditDefinition()
	s, ui, live := newCustomEditUI(t, def)
	before := live.RunTarget().Provider
	if err := s.Registry.SetRouteProtocol(t.Context(), def.Name, def.DefaultModel, llm.ModelProtocols.PROTOCOLMESSAGES); err != nil {
		t.Fatal(err)
	}
	customEditReplace(t, ui, def.Name, "renamed-proxy")
	requireRouteView(t, ui, "rename creates a new connection: keys and per-alias policies are not moved")
	if strings.Contains(ansi.Strip(ui.View().Content), "custom-edit-fake-key") {
		t.Fatal("form displayed credential plaintext")
	}
	customEditSave(t, ui)
	if _, err := s.Store.GetProvider(t.Context(), def.Name); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("old provider after rename = %v; want absent", err)
	}
	requireCustomEditKey(t, s, def.Name)
	keys, err := s.Svc.ListKeys(t.Context(), prefs.ScopeGlobal)
	if err != nil || slices.Contains(keys, "renamed-proxy") {
		t.Fatalf("rename migrated credential: keys=%v, err=%v", keys, err)
	}
	if _, err := s.Store.GetRouteProtocol(t.Context(), def.Name, def.DefaultModel); !errors.Is(err, backends.ErrRouteProtocolNotFound) {
		t.Fatalf("old route policy after rename = %v; want absent", err)
	}
	requireRoutePolicy(t, s, "renamed-proxy", def.DefaultModel, llm.ModelProtocols.PROTOCOLUNKNOWN)
	def.Name = "renamed-proxy"
	requireCustomEditDefinition(t, s, def)
	if live.RunTarget().Provider != before {
		t.Fatal("rename rebuilt the active target")
	}
}
