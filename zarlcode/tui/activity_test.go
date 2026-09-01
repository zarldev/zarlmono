package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestRunActivityUsesBrailleGlyphs(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	idle := ansi.Strip(model.View().Content)
	if !strings.Contains(idle, "○ idle") {
		t.Fatalf("idle UI missing idle activity indicator:\n%s", idle)
	}

	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "work"})
	running := ansi.Strip(model.View().Content)
	if !strings.Contains(running, "running") || !containsBraille(running) {
		t.Fatalf("running UI missing braille activity indicator:\n%s", running)
	}
}

func containsBraille(s string) bool {
	for _, r := range s {
		if r >= '\u2800' && r <= '\u28ff' {
			return true
		}
	}
	return false
}
