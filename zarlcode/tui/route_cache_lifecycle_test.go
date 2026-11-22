package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestRouteCacheEditorWorksBeforeRuntimeIsAvailable(t *testing.T) {
	s, ui, _, spec := newCacheEditorUI(t)
	ui.SetLiveRunner(nil)
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	cacheChoice(t, ui, 1)
	got, err := s.Registry.GetRouteCachePolicy(t.Context(), spec.Name, spec.Model)
	if err != nil || got.Mode != backends.RouteCacheModes.CACHEDEFAULT {
		t.Fatalf("offline settings save = %+v: %v", got, err)
	}
	closeCacheEditor(t, ui)
}

func TestRouteCacheEditorPreservesSavedDefaultLifetime(t *testing.T) {
	s, ui, _, spec := newCacheEditorUI(t)
	original := backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, System: true}
	if err := s.Registry.SetRouteCachePolicy(t.Context(), spec.Name, spec.Model, original); err != nil {
		t.Fatal(err)
	}
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	got, err := s.Registry.GetRouteCachePolicy(t.Context(), spec.Name, spec.Model)
	if err != nil || got != original {
		t.Fatalf("confirming existing policy changed its saved bytes: %+v: %v", got, err)
	}
	closeCacheEditor(t, ui)
}
