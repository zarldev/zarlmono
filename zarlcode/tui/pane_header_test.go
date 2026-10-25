package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

// The header shows session identity on the left and the active model on the right.
func TestHeaderPaneRightAlignsModel(t *testing.T) {
	const width = 60
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "claude-opus")
	model, _ := ui.Update(tea.WindowSizeMsg{Width: width, Height: 10})
	plain := ansi.Strip(model.View().Content)
	line, _, _ := strings.Cut(plain, "\n")

	if !strings.Contains(line, "claude-opus") {
		t.Fatalf("model name missing from header:\n%q", line)
	}
	end := ansi.StringWidth(strings.TrimRight(line, " "))
	if end != width-2 || strings.Contains(line, "follow") {
		t.Errorf("model should use the quiet header's right edge: %q", line)
	}
}
