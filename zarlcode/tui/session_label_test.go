package tui_test

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestFirstPromptNamesSessionAfterDraftAssignedIdentity(t *testing.T) {
	workspace := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspace))
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 200, Height: 50})

	prompt := "Fix   generated session labels after draft autosave"
	model, debounceCmd := model.Update(tea.PasteMsg{Content: prompt})
	if debounceCmd == nil {
		t.Fatal("typing first prompt did not schedule draft persistence")
	}
	model, persistCmd := model.Update(debounceCmd())
	if persistCmd == nil {
		t.Fatal("draft debounce did not start persistence")
	}
	// Identity is assigned before the persistence command runs. This is the
	// state that previously suppressed generation of the first-prompt label.

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	model, _ = model.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'n'})
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "Fix generated session labels after draft autosave") {
		t.Fatalf("first prompt did not generate a normalized session name:\n%s", out)
	}
}
