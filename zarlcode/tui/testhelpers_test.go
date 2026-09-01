package tui_test

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func drive(t *testing.T, msgs ...tea.Msg) string {
	t.Helper()
	var m tea.Model = tui.New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return ansi.Strip(m.View().Content)
}

func step(t *testing.T, m *tui.UI, msg tea.Msg) {
	t.Helper()
	got, _ := m.Update(msg)
	if got != m {
		t.Fatalf("Update returned %T", got)
	}
}

func window(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }
func textKey(s string) tea.KeyPressMsg  { return tea.KeyPressMsg{Text: s, Code: []rune(s)[0]} }
func typeAndEnter(t *testing.T, m *tui.UI, s string) {
	t.Helper()
	step(t, m, textKey(s))
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func newTestSettings(t *testing.T) *engine.Settings {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return engine.NewSettings(store, nil, nil, dir)
}

func newTestSettingsAt(t *testing.T, dir string) *engine.Settings {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return engine.NewSettings(store, nil, nil, dir)
}
