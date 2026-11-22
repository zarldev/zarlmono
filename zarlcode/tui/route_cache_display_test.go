package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestRouteCacheEditorPreservesOpaqueNewlines(t *testing.T) {
	s, ui, _, _ := newCacheEditorUI(t)
	openCacheEditor(t, ui, true)
	step(t, ui, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	const alias = "team/line\r\nAlias"
	step(t, ui, tea.PasteMsg{Content: alias})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	cacheChoice(t, ui, 1)
	got, err := s.Registry.GetRouteCachePolicy(t.Context(), "proxy Δ", alias)
	if err != nil || got.Mode != backends.RouteCacheModes.CACHEDEFAULT {
		t.Fatalf("opaque alias was changed: %+v: %v", got, err)
	}
	closeCacheEditor(t, ui)
}

func TestRouteCacheEditorScrollsWithinSettings(t *testing.T) {
	_, ui, _, _ := newCacheEditorUI(t)
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	step(t, ui, window(90, 18))
	before := ui.View().Content
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if after := ui.View().Content; after == before {
		t.Fatal("cache editor did not scroll its settings detail surface")
	}
	closeCacheEditor(t, ui)
}
