package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

// The header bar shows app/mode on the left and the active model on the right.
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
	end := strings.Index(line, "claude-opus") + len("claude-opus")
	viewport := strings.Index(line, "follow 100%")
	if viewport < 0 || viewport-end > 6 {
		t.Errorf("model name should be pinned beside the right-edge viewport state, model end %d viewport start %d:\n%q", end, viewport, line)
	}
}
