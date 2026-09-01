package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestNestedDisclosureThroughTranscriptNavigation(t *testing.T) {
	m := tui.New()
	step(t, m, window(120, 30))
	step(t, m, teasink.ToolStartedMsg{TaskID: "t", ToolID: "p", ToolName: "program"})
	step(t, m, teasink.ToolStartedMsg{TaskID: "t", ToolID: "r", ParentToolID: "p", ToolName: "read"})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "program") {
		t.Fatalf("program disclosure missing:\n%s", out)
	}
}
