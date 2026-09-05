package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestIntroAcceptsNormalizedMultilinePaste(t *testing.T) {
	ui := tui.New()
	ui.OpenIntro("/tmp/ws")
	model, _ := ui.Update(tea.PasteMsg{Content: "hello\r\nworld"})
	ui = model.(*tui.UI)
	if got := ui.IntroPrompt(); got != "hello\nworld" {
		t.Fatalf("intro prompt = %q", got)
	}
}
