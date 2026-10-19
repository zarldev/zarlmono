package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestMouseDrawerResizePreservesSelectedSiblingIdentity(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 100})
	for _, entry := range []struct{ id, value string }{{"a", strings.Repeat("EARLIER_ARGUMENT\n", 18)}, {"b", "SELECTED_B_MARKER"}, {"c", "UNSELECTED_C_MARKER"}} {
		ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: entry.id, ToolID: entry.id, ToolName: "inspect", Parameters: map[string]any{"extra": entry.value}})
	}
	expandFirstTool(ui)
	toolViewKey(ui, tea.KeyDown)
	toolViewKey(ui, tea.KeyEnter)       // Select and expand B after A's long body.
	clickToolLabel(t, ui, "arguments:") // Collapse A, moving B and C upwards.
	toolViewKey(ui, 'j')
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, `"extra": "SELECTED_B_MARKER"`) || strings.Contains(view, "UNSELECTED_C_MARKER") {
		t.Fatalf("mouse body resize retargeted selected sibling: %s", view)
	}
}
