package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestRouteCacheEditorRuntimeOverrideMasksSavedRow(t *testing.T) {
	s, ui, live, _ := newCacheEditorUI(t)
	spec := live.RunTarget().Spec
	spec.CachePolicy = backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}
	provider, err := engine.BuildProvider(t.Context(), s.Registry, s.Svc, spec)
	if err != nil {
		t.Fatal(err)
	}
	live.SetProviderSpec(provider, spec)
	ui.SetProviderContext(spec, spec)
	before := live.RunTarget().Provider
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireCacheView(t, ui, "Runtime cache override wins for this run")
	cacheChoice(t, ui, 1)
	closeCacheEditor(t, ui)
	if live.RunTarget().Provider != before || live.RunTarget().Spec != spec {
		t.Fatal("saved row displaced runtime override")
	}
	saved, err := s.Registry.GetRouteCachePolicy(t.Context(), spec.Name, spec.Model)
	if err != nil || saved.Mode != backends.RouteCacheModes.CACHEDEFAULT {
		t.Fatalf("saved = %+v %v", saved, err)
	}
}
