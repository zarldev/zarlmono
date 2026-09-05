package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestHeaderExposesModelRunAndViewportState(t *testing.T) {
	ui := tui.New()
	ui.SetWorkspace("/tmp/project", "qwen3")
	model, _ := ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui = model.(*tui.UI)
	title := strings.SplitN(ansi.Strip(ui.View().Content), "\n", 2)[0]
	for _, want := range []string{"ƶ", "qwen3", "idle", "follow"} {
		if !strings.Contains(title, want) {
			t.Errorf("header missing %q: %s", want, title)
		}
	}
}
