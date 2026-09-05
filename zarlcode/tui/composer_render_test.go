package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestComposerWrapPreservesInputCharacters(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	ui := tui.New()
	model, _ := ui.Update(tea.WindowSizeMsg{Width: 20, Height: 12})
	ui = model.(*tui.UI)
	for _, r := range alphabet {
		model, _ = ui.Update(tea.KeyPressMsg{Text: string(r), Code: r})
		ui = model.(*tui.UI)
	}
	plain := ansi.Strip(ui.View().Content)
	for _, r := range alphabet {
		if !strings.ContainsRune(plain, r) {
			t.Errorf("wrapped composer dropped %q:\n%s", r, plain)
		}
	}
}
