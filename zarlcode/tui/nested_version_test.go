package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestNestedToolCompletionRendersImmediately(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "t", Depth: 0})
	model, _ = model.Update(teasink.ToolStartedMsg{TaskID: "t", ToolID: "program", ToolName: "program", Parameters: map[string]any{"description": "nested"}})
	model, _ = model.Update(teasink.ToolStartedMsg{TaskID: "t", ToolID: "child", ToolName: "grep", Parameters: map[string]any{"pattern": "needle"}, ParentToolID: "program"})

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = model.View()

	model, _ = model.Update(teasink.ToolCompletedMsg{TaskID: "t", ToolID: "child", ToolName: "grep", FormattedResult: "child completed result", Duration: time.Millisecond, ParentToolID: "program"})
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "✓ grep") {
		t.Fatalf("nested completion was not rendered immediately:\n%s", out)
	}
}
