package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestComposerEditsAndRendersMultilineInput(t *testing.T) {
	ui := tui.New()
	step := func(msg tea.Msg) { model, _ := ui.Update(msg); ui = model.(*tui.UI) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(tea.KeyPressMsg{Text: "a", Code: 'a'})
	step(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	step(tea.PasteMsg{Content: "b\r\nc"})
	if got := ui.ComposerText(); got != "a\nb\nc" {
		t.Fatalf("composer text = %q", got)
	}
	out := ansi.Strip(ui.View().Content)
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

func TestComposerSubmitEchoesWithoutRunner(t *testing.T) {
	ui := tui.New()
	model, _ := ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui = model.(*tui.UI)
	for _, r := range "echo this prompt" {
		model, _ = ui.Update(tea.KeyPressMsg{Text: string(r), Code: r})
		ui = model.(*tui.UI)
	}
	model, _ = ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui = model.(*tui.UI)
	if out := ansi.Strip(ui.View().Content); !strings.Contains(out, "echo this prompt") {
		t.Fatalf("submitted prompt not echoed:\n%s", out)
	}
}
