package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestComposer_Editing(t *testing.T) {
	var c composer
	c.insert("hello")
	if c.text() != "hello" {
		t.Fatalf("insert: %q", c.text())
	}
	c.backspace()
	if c.text() != "hell" {
		t.Fatalf("backspace: %q", c.text())
	}
	c.left()
	c.insert("X")
	if c.text() != "helXl" {
		t.Fatalf("mid-insert: %q", c.text())
	}
	if got := c.submit(); got != "helXl" || c.text() != "" {
		t.Fatalf("submit: got %q, remaining %q", got, c.text())
	}
}

func TestComposer_TypingShowsInEditor(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 200, Height: 50})
	for _, ch := range []string{"h", "i", " ", "t", "h", "e", "r", "e"} {
		step(tea.KeyPressMsg{Text: ch, Code: []rune(ch)[0]})
	}
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "hi there") {
		t.Errorf("typed text not shown in editor:\n%s", out)
	}
}

func TestComposer_MultilineAndPaste(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 200, Height: 50})
	step(tea.KeyPressMsg{Text: "a", Code: 'a'})
	step(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	step(tea.PasteMsg{Content: "b\r\nc"})

	if got := m.composer.text(); got != "a\nb\nc" {
		t.Fatalf("composer text = %q, want normalized multiline paste", got)
	}
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(out, want) {
			t.Fatalf("composer multiline render missing %q:\n%s", want, out)
		}
	}
}

func TestComposer_MultilineInsertKeysMatchIntro(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{name: "shift enter", msg: tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}},
		{name: "ctrl enter", msg: tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}},
		{name: "alt enter", msg: tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}},
		{name: "ctrl j", msg: tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
			step(tea.WindowSizeMsg{Width: 200, Height: 50})
			step(tea.KeyPressMsg{Text: "a", Code: 'a'})
			step(tt.msg)
			step(tea.KeyPressMsg{Text: "b", Code: 'b'})

			if got := m.composer.text(); got != "a\nb" {
				t.Fatalf("composer text = %q, want %q", got, "a\nb")
			}
		})
	}
}

func TestComposer_ExpandsForMultipleLines(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 200, Height: 50})

	initialHeight := m.layout.editor.Dy()
	for i, ch := range []string{"a", "b", "c"} {
		if i > 0 {
			step(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
		}
		step(tea.KeyPressMsg{Text: ch, Code: []rune(ch)[0]})
	}

	if got := m.layout.editor.Dy(); got <= initialHeight {
		t.Fatalf("editor height = %d, want > initial height %d", got, initialHeight)
	}
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded composer render missing %q:\n%s", want, out)
		}
	}
}

func TestComposer_SubmitEchoesWithoutRunner(t *testing.T) {
	m := New()
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = mm.(*UI)
	m.submit("echo this prompt")
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "echo this prompt") {
		t.Errorf("submit without a runner should echo a user item:\n%s", out)
	}
}

func TestComposer_InputHistoryNavigatesSubmittedPrompts(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 200, Height: 50})

	typeComposerText(t, step, "first")
	step(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeComposerText(t, step, "second")
	step(tea.KeyPressMsg{Code: tea.KeyEnter})

	step(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.composer.text(); got != "second" {
		t.Fatalf("first up = %q, want newest prompt", got)
	}
	step(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.composer.text(); got != "first" {
		t.Fatalf("second up = %q, want older prompt", got)
	}
	step(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.composer.text(); got != "first" {
		t.Fatalf("up at oldest = %q, want oldest prompt", got)
	}
	step(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.composer.text(); got != "second" {
		t.Fatalf("down = %q, want newer prompt", got)
	}
	step(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.composer.text(); got != "" {
		t.Fatalf("down past newest = %q, want empty draft", got)
	}
}

func TestComposer_InputHistoryRestoresDraft(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 200, Height: 50})

	typeComposerText(t, step, "submitted")
	step(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeComposerText(t, step, "draft")
	step(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.composer.text(); got != "submitted" {
		t.Fatalf("up = %q, want submitted prompt", got)
	}
	step(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.composer.text(); got != "draft" {
		t.Fatalf("down = %q, want restored draft", got)
	}
}

func TestComposer_InputHistoryEditingRecallStartsFresh(t *testing.T) {
	m := New()
	step := func(msg tea.Msg) { mm, _ := m.Update(msg); m = mm.(*UI) }
	step(tea.WindowSizeMsg{Width: 200, Height: 50})

	typeComposerText(t, step, "submitted")
	step(tea.KeyPressMsg{Code: tea.KeyEnter})
	step(tea.KeyPressMsg{Code: tea.KeyUp})
	typeComposerText(t, step, " edited")
	step(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.composer.text(); got != "submitted edited" {
		t.Fatalf("down after editing recalled prompt = %q, want unchanged edited prompt", got)
	}
}

func TestComposer_EscDoesNotQuitWhenIdle(t *testing.T) {
	m := New()
	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil {
		t.Fatalf("idle esc returned a command; esc should not quit")
	}
	if m.overlay.active() {
		t.Fatal("idle esc should not open the quit confirmation dialog")
	}
}

func TestComposer_CtrlCUsesQuitConfirmation(t *testing.T) {
	m := New()
	m.session.ConfirmQuit = true

	cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("ctrl+c with confirm_quit returned a command; want dialog")
	}
	if !m.overlay.active() {
		t.Fatal("ctrl+c should open the quit confirmation dialog")
	}
	if _, ok := m.overlay.top().(*quitConfirmDialog); !ok {
		t.Fatalf("ctrl+c opened %T, want *quitConfirmDialog", m.overlay.top())
	}
}

func TestComposer_CtrlQUsesConversationDialog(t *testing.T) {
	m := New()

	cmd := m.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("ctrl+q returned a command; want dialog")
	}
	if !m.overlay.active() {
		t.Fatal("ctrl+q should open the conversation dialog")
	}
	if _, ok := m.overlay.top().(*conversationActionsDialog); !ok {
		t.Fatalf("ctrl+q opened %T, want *conversationActionsDialog", m.overlay.top())
	}
}

func TestComposer_ContextViewScrollKeys(t *testing.T) {
	m := New()
	m.width, m.height = 100, 14
	m.layout = computeLayout(m.width, m.height)
	for i := range 20 {
		m.session.Run.foldTurnComplete(&llm.Usage{PromptTokens: 500 + i, CompletionTokens: 50, TotalTokens: 550 + i}, time.Second, 1)
	}
	maxScroll := m.dashboardMaxScroll()
	if maxScroll == 0 {
		t.Fatal("test setup should produce an overflowing context view")
	}

	m.handleKey(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if !m.session.CockpitExpanded {
		t.Fatal("ctrl+l should open the context view in session state")
	}
	if m.contextView.activeScroll() != 0 {
		t.Fatalf("context view should open at top, got scroll %d", m.contextView.activeScroll())
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.contextView.activeScroll() <= 0 {
		t.Fatalf("pgdown should scroll context view, got %d", m.contextView.activeScroll())
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.contextView.activeScroll() != maxScroll {
		t.Fatalf("end scroll = %d, want %d", m.contextView.activeScroll(), maxScroll)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.contextView.activeScroll() != maxScroll {
		t.Fatalf("context view scroll should clamp at max %d, got %d", maxScroll, m.contextView.activeScroll())
	}
	m.handleKey(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if m.session.CockpitExpanded {
		t.Fatal("ctrl+l should close the context view when focused")
	}
}

func typeComposerText(t *testing.T, step func(tea.Msg), s string) {
	t.Helper()
	for _, ch := range s {
		step(tea.KeyPressMsg{Text: string(ch), Code: ch})
	}
}
