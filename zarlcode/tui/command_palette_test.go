package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestCommandPaletteAcceptsPastedSearchText(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'k'})
	model, _ = model.Update(tea.PasteMsg{Content: "copy last response"})

	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "› copy last response") {
		t.Fatalf("command palette dropped pasted query:\n%s", out)
	}
	if !strings.Contains(out, "Copy last response") {
		t.Fatalf("pasted query did not filter to copy command:\n%s", out)
	}
	if strings.Contains(out, "Open settings") {
		t.Fatalf("pasted query did not filter unrelated commands:\n%s", out)
	}
}
