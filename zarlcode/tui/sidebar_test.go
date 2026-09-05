package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestSidebarShowsRunState(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "do the thing"})
	out := ansi.Strip(model.View().Content)
	for _, want := range []string{"context", "run", "running", "provider", "model"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q:\n%s", want, out)
		}
	}
}
