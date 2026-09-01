package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func renderFailedTool(t *testing.T, kind tools.Kind) string {
	t.Helper()
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "t1", Depth: 0})
	model, _ = model.Update(teasink.ToolStartedMsg{TaskID: "t1", ToolID: "call1", ToolName: "read", Parameters: map[string]any{"path": "missing.go"}})
	model, _ = model.Update(teasink.ToolFailedMsg{TaskID: "t1", ToolID: "call1", ToolName: "read", Error: "no such file", Kind: kind, Duration: time.Millisecond})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return ansi.Strip(model.View().Content)
}

func TestToolFailureRendersKindBadge(t *testing.T) {
	out := renderFailedTool(t, tools.Kinds.NOTFOUND)
	if !strings.Contains(out, "[not_found]") {
		t.Errorf("classified failure should render its kind badge, got:\n%s", out)
	}
}

func TestToolFailureUnknownKindHasNoBadge(t *testing.T) {
	out := renderFailedTool(t, tools.Kinds.UNKNOWN)
	if strings.Contains(out, "[unknown]") {
		t.Errorf("unclassified failure must not render a kind badge, got:\n%s", out)
	}
}
