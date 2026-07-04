package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestOverlay_PushPop(t *testing.T) {
	var o overlay
	if o.active() {
		t.Fatal("empty overlay should be inactive")
	}
	o.push(newHelpDialog())
	if !o.active() {
		t.Fatal("push should activate")
	}
	o.pop()
	if o.active() {
		t.Fatal("pop should deactivate")
	}
	o.pop() // popping empty must not panic
}

func TestHelpDialog_RendersAndDismisses(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 120, Height: 50})

	m.overlay.push(newHelpDialog())
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "keys") || !strings.Contains(out, "submit prompt") {
		t.Errorf("help dialog not rendered:\n%s", out)
	}
	if !strings.Contains(out, "stop current turn") || !strings.Contains(out, "ctrl+c") || strings.Contains(out, "stop run / quit") {
		t.Errorf("help dialog should document esc as turn management and ctrl+c as quit:\n%s", out)
	}

	m.overlay.pop()
	if strings.Contains(ansi.Strip(m.View().Content), "submit prompt") {
		t.Error("help content should be gone after pop")
	}
}

func TestHelpDialog_IsTailoredToCompose(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 160, Height: 80})

	m.overlay.push(m.newHelpDialog())
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"compose", "quick panes", "slash commands", "global", "submit prompt", "file viewer",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compose help missing %q:\n%s", want, out)
		}
	}
	for _, notWant := range []string{
		"startup", "viewers / pickers", "prompt ⇄ sessions",
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("compose help should not include %q:\n%s", notWant, out)
		}
	}
	for _, cmd := range slashCommands {
		if n := strings.Count(out, cmd.name); n != 1 {
			t.Fatalf("help dialog renders slash command %q %d times, want once:\n%s", cmd.name, n, out)
		}
	}
}

func TestHelpDialog_IsTailoredToBrowse(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.timeline.enterBrowse()

	m.overlay.push(m.newHelpDialog())
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"browse transcript", "expand / collapse", "back to compose"} {
		if !strings.Contains(out, want) {
			t.Fatalf("browse help missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "submit prompt") || strings.Contains(out, "slash commands") {
		t.Fatalf("browse help should omit compose-only help:\n%s", out)
	}
}

func TestUI_CtrlGTogglesHelp(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	ctrlG := tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'g'}
	step(ctrlG)
	if !m.overlay.active() {
		t.Fatalf("ctrl+g should open help (key string = %q)", ctrlG.String())
	}
	step(ctrlG)
	if m.overlay.active() {
		t.Fatal("ctrl+g again should close help")
	}
}

func TestUI_DialogInterceptsTyping(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	m.overlay.push(newHelpDialog())
	step(tea.KeyPressMsg{Text: "x", Code: 'x'})
	if m.composer.text() != "" {
		t.Errorf("composer must not receive input while a dialog is open, got %q", m.composer.text())
	}
}
