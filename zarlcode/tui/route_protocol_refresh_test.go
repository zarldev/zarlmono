package tui_test

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

var errRouteEditorStorage = errors.New("route editor storage unavailable")

type routeEditorStore struct {
	backends.RouteProtocolStore
	readErr, writeErr, deleteErr error
}

func (s routeEditorStore) GetRouteProtocol(ctx context.Context, provider, alias string) (llm.ModelProtocol, error) {
	if s.readErr != nil {
		return llm.ModelProtocols.PROTOCOLUNKNOWN, s.readErr
	}
	return s.RouteProtocolStore.GetRouteProtocol(ctx, provider, alias)
}
func (s routeEditorStore) SetRouteProtocol(ctx context.Context, provider, alias string, protocol llm.ModelProtocol) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.RouteProtocolStore.SetRouteProtocol(ctx, provider, alias, protocol)
}
func (s routeEditorStore) DeleteRouteProtocol(ctx context.Context, provider, alias string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.RouteProtocolStore.DeleteRouteProtocol(ctx, provider, alias)
}

func TestRouteProtocolEditorStorageFailures(t *testing.T) {
	for _, failure := range []string{"no store", "read", "write", "delete"} {
		t.Run(failure, func(t *testing.T) {
			s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
			before := live.RunTarget().Provider
			store := routeEditorStore{RouteProtocolStore: s.Store}
			initial := llm.ModelProtocols.PROTOCOLUNKNOWN
			if failure == "delete" {
				initial = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
				if err := s.Registry.SetRouteProtocol(t.Context(), spec.Name, spec.Model, initial); err != nil {
					t.Fatal(err)
				}
				store.deleteErr = errRouteEditorStorage
			}
			if failure == "read" {
				store.readErr, store.writeErr = errRouteEditorStorage, errRouteEditorStorage
			}
			if failure == "write" {
				store.writeErr = errRouteEditorStorage
			}
			if failure == "no store" {
				s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(routeEditorDefinitions()))
			} else {
				s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(routeEditorDefinitions()), backends.WithRouteProtocolStore(store))
			}
			openRouteEditor(t, ui, false)
			step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
			from, to := 0, 2
			switch failure {
			case "no store":
				requireRouteView(t, ui, "Durable storage unavailable; saved policy unknown.")
			case "read":
				requireRouteView(t, ui, "Saved policy unavailable:")
			case "delete":
				from, to = 1, 0
			}
			saveRouteChoice(t, ui, from, to)
			requireRouteView(t, ui, "Policy not saved:")
			if failure == "delete" {
				requireRouteView(t, ui, "Chat Completions [saved]")
			}
			closeRouteEditor(t, ui)
			if live.RunTarget().Provider != before {
				t.Fatal("failed persistence changed active target")
			}
			// Read the real durable store, not the failing wrapper.
			s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(routeEditorDefinitions()), backends.WithRouteProtocolStore(s.Store))
			requireRoutePolicy(t, s, spec.Name, spec.Model, initial)
			requireSelection(t, s, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: spec.Name, Model: spec.Model})
		})
	}
}

func saveResponsesPolicy(t *testing.T, ui *tui.UI) {
	t.Helper()
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	saveRouteChoice(t, ui, 0, 2)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEscape})
}

func TestRouteProtocolEditorBuildFailureIsDurableAndRetryable(t *testing.T) {
	s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	before := live.RunTarget().Provider
	saveResponsesPolicy(t, ui)
	_, cmd := ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	registry := s.Registry
	s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(nil))
	runSettingsCloseCmd(t, ui, cmd)
	s.Registry = registry
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLRESPONSES)
	if live.RunTarget().Provider != before {
		t.Fatal("build failure replaced old target")
	}
	// The failure remains visible after the transient root toast expires.
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireRouteView(t, ui, "Policy saved; current provider not refreshed:")
	closeRouteEditor(t, ui)
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLRESPONSES)
	requireSelection(t, s, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: spec.Name, Model: spec.Model})
	// A successful refresh clears only this pending pair and avoids a second rebuild.
	before = live.RunTarget().Provider
	closeSettings(t, ui)
	if live.RunTarget().Provider != before {
		t.Fatal("successful refresh left a spurious pending retry")
	}
}

func TestRouteProtocolEditorAdmissionFailureKeepsOldRuntimeAndRetries(t *testing.T) {
	s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	before := live.RunTarget().Provider
	saveResponsesPolicy(t, ui)
	_, cmd := ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	result := cmd()
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reservation.Release)
	step(t, ui, result)
	requireRouteView(t, ui, "Policy saved; current provider not refreshed:")
	if live.RunTarget().Provider != before {
		t.Fatal("admission failure changed live target")
	}
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLRESPONSES)
	reservation.Release()
	// A pending policy retries on settings close, not when unrelated dialogs close.
	step(t, ui, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	_, unrelatedClose := ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	runSettingsCloseCmd(t, ui, unrelatedClose)
	if live.RunTarget().Provider != before {
		t.Fatal("unrelated dialog close retried policy application")
	}
	closeSettings(t, ui)
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLRESPONSES)
}

func TestRouteProtocolEditorRejectsPreEditManualAndPolicyCompletions(t *testing.T) {
	for _, source := range []string{"manual", "policy"} {
		t.Run(source, func(t *testing.T) {
			s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
			before := live.RunTarget().Provider
			var stale tea.Msg
			if source == "manual" {
				stale = routeManualBuild(t, ui, spec.Name, spec.Model)()
			} else {
				saveResponsesPolicy(t, ui)
				_, cmd := ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
				stale = cmd()
			}
			openRouteEditor(t, ui, false)
			step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
			from := 0
			if source == "policy" {
				from = 2
			}
			saveRouteChoice(t, ui, from, 3)
			step(t, ui, stale)
			if live.RunTarget().Provider != before {
				t.Fatal("pre-edit completion published stale policy")
			}
			closeRouteEditor(t, ui)
			requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLMESSAGES)
			requireSelection(t, s, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: spec.Name, Model: spec.Model})
		})
	}
}

func TestRouteProtocolEditorPreservesPlanAndManualRouting(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "mode route", true: "manual override"}[manual], func(t *testing.T) {
			s, ui, live, primary := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
			setPlanRoute(t, s, primary.Name, "plan/alias")
			step(t, ui, routeToggleMode(t, ui)())
			active := live.RunTarget().Spec
			if manual {
				step(t, ui, routeManualBuild(t, ui, primary.Name, "manual/alias")())
				active = live.RunTarget().Spec
			}
			selection := s.ActiveProvider(t.Context(), primary)
			openRouteEditor(t, ui, false)
			step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
			saveRouteChoice(t, ui, 0, 3)
			closeRouteEditor(t, ui)
			requireRouteRuntime(t, live, active, llm.ModelProtocols.PROTOCOLMESSAGES)
			if got := s.ActiveProvider(t.Context(), primary); got != selection {
				t.Fatalf("refresh changed selection: %+v -> %+v", selection, got)
			}
			// Real mode transitions must continue routing after a policy refresh.
			if !manual {
				setPlanRoute(t, s, primary.Name, "next/plan")
				runSettingsCloseCmd(t, ui, routeToggleMode(t, ui))
				step(t, ui, routeToggleMode(t, ui)())
				if live.RunTarget().Spec.Model != "next/plan" {
					t.Fatal("protocol refresh introduced a manual route override")
				}
			} else {
				closeSettings(t, ui)
				requireRouteRuntime(t, live, active, llm.ModelProtocols.PROTOCOLMESSAGES)
			}
		})
	}
}

func TestRouteProtocolEditorInvalidatesModeBuildAndPreparedTarget(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "in flight", true: "prepared"}[prepared], func(t *testing.T) {
			s, ui, live, primary := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
			before := live.RunTarget().Provider
			setPlanRoute(t, s, primary.Name, "plan/alias")
			stale := routeToggleMode(t, ui)()
			if prepared {
				reservation, err := live.ReserveRuntime()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(reservation.Release)
				step(t, ui, stale)
				reservation.Release()
			}
			openRouteEditor(t, ui, false)
			step(t, ui, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
			step(t, ui, tea.PasteMsg{Content: "plan/alias"})
			step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
			saveRouteChoice(t, ui, 0, 2)
			if !prepared {
				step(t, ui, stale)
			}
			if live.RunTarget().Provider != before {
				t.Fatal("stale mode policy replaced active runtime")
			}
			closeRouteEditor(t, ui)
			want := primary
			want.Model = "plan/alias"
			requireRouteRuntime(t, live, want, llm.ModelProtocols.PROTOCOLRESPONSES)
			requireSelection(t, s, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: primary.Name, Model: primary.Model})
		})
	}
}

func TestRouteProtocolEditorDefaultAliasRefreshKeepsSpecUnchanged(t *testing.T) {
	s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	spec.Model = ""
	provider, err := engine.BuildProvider(t.Context(), s.Registry, s.Svc, spec)
	if err != nil {
		t.Fatal(err)
	}
	live.SetProviderSpec(provider, spec)
	ui.SetProviderContext(spec, spec)
	openRouteEditor(t, ui, false)
	requireRouteView(t, ui, `Alias: "fallback/alias"`)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	saveRouteChoice(t, ui, 0, 3)
	closeRouteEditor(t, ui)
	requireRoutePolicy(t, s, spec.Name, "fallback/alias", llm.ModelProtocols.PROTOCOLMESSAGES)
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLMESSAGES)
	facts := live.RunTarget().Facts
	if facts == nil || facts.RequestedAlias != "fallback/alias" || facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLMESSAGES {
		t.Fatalf("default alias construction facts lost during policy refresh: %+v", facts)
	}
}
