package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestProviderIdentityRendersAsWords(t *testing.T) {
	ui := tui.New()
	ui.SetProvider("llamacpp")
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "llamacpp") {
		t.Fatalf("provider identity missing from UI:\n%s", out)
	}
}
