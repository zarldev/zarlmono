package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestRouteCacheEditorAdmissionFailureRetryAndStaleBuild(t *testing.T) {
	s, ui, live, spec := newCacheEditorUI(t)
	before := live.RunTarget().Provider
	stale := routeManualBuild(t, ui, spec.Name, spec.Model)()
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	cacheChoice(t, ui, 2)
	step(t, ui, stale)
	if live.RunTarget().Provider != before {
		t.Fatal("pre-edit build replaced runtime")
	}
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEscape})
	_, cmd := ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	result := cmd()
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reservation.Release)
	step(t, ui, result)
	if live.RunTarget().Provider != before {
		t.Fatal("failed admission replaced runtime")
	}
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireCacheView(t, ui, "saved not applied; retry")
	closeCacheEditor(t, ui)
	reservation.Release()
	closeSettings(t, ui)
	if live.RunTarget().Provider == before {
		t.Fatal("retry did not refresh")
	}
	saved, err := s.Registry.GetRouteCachePolicy(t.Context(), spec.Name, spec.Model)
	if err != nil || saved.Mode != backends.RouteCacheModes.CACHEDISABLED {
		t.Fatalf("saved = %+v %v", saved, err)
	}
}

func TestRouteCacheEditorUnsupportedProtocolOffersOnlyClearDefault(t *testing.T) {
	s, ui, _, spec := newCacheEditorUI(t)
	if err := s.Registry.SetRouteProtocol(t.Context(), spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLRESPONSES); err != nil {
		t.Fatal(err)
	}
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireCacheView(t, ui, "Resolved protocol for choices: responses")
	requireCacheView(t, ui, "Save factory default")
	view := ui.View().Content
	for _, unexpected := range []string{"Chat system", "Messages static", "Disable generated"} {
		if contains := strings.Contains(view, unexpected); contains {
			t.Fatalf("unsupported choice %q offered", unexpected)
		}
	}
	closeCacheEditor(t, ui)
}
