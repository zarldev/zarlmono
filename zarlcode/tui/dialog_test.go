package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestHelpDialogOpensScrollsAndDismisses(t *testing.T) {
	m := tui.New()
	step(t, m, window(80, 10))
	ctrlG := tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'g'}
	step(t, m, ctrlG)
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"[keys]", "contextual shortcuts", "submit prompt", "↑↓/jk scroll", "ctrl+g / esc close"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if out = ansi.Strip(m.View().Content); !strings.Contains(out, "global") {
		t.Fatalf("end did not reveal final section:\n%s", out)
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	step(t, m, ctrlG)
	if strings.Contains(ansi.Strip(m.View().Content), "contextual shortcuts") {
		t.Fatal("ctrl+g did not dismiss help")
	}
}

func TestDialogInterceptsTyping(t *testing.T) {
	m := tui.New()
	step(t, m, window(120, 30))
	step(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'g'})
	step(t, m, textKey("x"))
	if got := m.ComposerText(); got != "" {
		t.Fatalf("composer received dialog input: %q", got)
	}
}

func TestConversationDialogsRenderSharedRegions(t *testing.T) {
	m := tui.New()
	step(t, m, window(100, 20))
	step(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'q'})
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"[conversation]", "context", "conversation context", "x clear…"} {
		if !strings.Contains(out, want) {
			t.Errorf("dialog missing %q:\n%s", want, out)
		}
	}
	step(t, m, textKey("x"))
	out = ansi.Strip(m.View().Content)
	for _, want := range []string{"[clear]", "reset conversation", "clear conversation context?", "any other key cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("confirmation missing %q:\n%s", want, out)
		}
	}
}
