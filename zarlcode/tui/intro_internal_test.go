package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func introTextKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: string(r), Code: r}
}

func TestIntroPromptTextKeysDoNotTriggerSessionNavigation(t *testing.T) {
	m := New()
	p := newIntroPane("/tmp/ws", []sessionSummary{{ID: "s1"}, {ID: "s2"}}, "", "")

	for _, r := range []rune{'k', 'K', 'j', 'g', 'G'} {
		p.handleKey(m, introTextKey(r))
	}

	if got := string(p.prompt); got != "kKjgG" {
		t.Fatalf("prompt text = %q, want kKjgG", got)
	}
	if p.cursor != 0 {
		t.Fatalf("session cursor moved while prompt focused: %d", p.cursor)
	}
}

func TestIntroPromptSupportsMultilineTypingAndPaste(t *testing.T) {
	m := New()
	p := newIntroPane("/tmp/ws", nil, "", "")

	p.handleKey(m, introTextKey('a'))
	p.handleKey(m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	p.paste("b\r\nc")

	if got := string(p.prompt); got != "a\nb\nc" {
		t.Fatalf("prompt text = %q, want multiline paste normalized", got)
	}
}

func TestIntroPasteMessageRoutesToPrompt(t *testing.T) {
	m := New()
	m.intro = newIntroPane("/tmp/ws", nil, "", "")

	mm, _ := m.Update(tea.PasteMsg{Content: "hello\nworld"})
	m = mm.(*UI)

	if got := string(m.intro.prompt); got != "hello\nworld" {
		t.Fatalf("intro prompt after PasteMsg = %q", got)
	}
}

func TestIntroCtrlCUsesQuitConfirmation(t *testing.T) {
	m := New()
	m.intro = newIntroPane("/tmp/ws", nil, "", "")
	m.session.ConfirmQuit = true

	cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("intro ctrl+c with confirm_quit returned a command; want dialog")
	}
	if !m.overlay.active() {
		t.Fatal("intro ctrl+c should open the quit confirmation dialog")
	}
	if _, ok := m.overlay.top().(*quitConfirmDialog); !ok {
		t.Fatalf("intro ctrl+c opened %T, want *quitConfirmDialog", m.overlay.top())
	}
}

func TestIntroCtrlQUsesConversationDialog(t *testing.T) {
	m := New()
	m.intro = newIntroPane("/tmp/ws", nil, "", "")

	cmd := m.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("intro ctrl+q returned a command; want dialog")
	}
	if !m.overlay.active() {
		t.Fatal("intro ctrl+q should open the conversation dialog")
	}
	if _, ok := m.overlay.top().(*conversationActionsDialog); !ok {
		t.Fatalf("intro ctrl+q opened %T, want *conversationActionsDialog", m.overlay.top())
	}
}

func TestIntroPromptRowsStayAlignedAfterTyping(t *testing.T) {
	p := newIntroPane("/tmp/ws", nil, "", "")
	p.insert("hello")

	lines := p.promptLines(80, false)
	if len(lines) < 3 {
		t.Fatalf("promptLines returned %d lines, want frame and body", len(lines))
	}
	want := ansi.StringWidth(lines[0])
	for i, line := range lines[1:] {
		if got := ansi.StringWidth(line); got != want {
			t.Fatalf("prompt row %d width = %d, want %d\nlines:\n%q", i+1, got, want, lines)
		}
	}
}
