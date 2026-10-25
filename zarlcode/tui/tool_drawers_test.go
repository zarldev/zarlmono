package tui_test

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func toolViewKey(ui *tui.UI, key rune) {
	message := tea.KeyPressMsg{Code: key}
	if key == 'a' || key == 'r' || key == 'j' {
		message.Text = string(key)
	}
	ui.Update(message)
}

func drawerFixture() *tui.UI {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 100})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", Parameters: map[string]any{"path": "INPUT_MARKER", "token": "SECRET_CANARY", "command": "echo first\necho second"}})
	expandFirstTool(ui)
	return ui
}

func TestToolArgumentsAndResultsCollapseIndependently(t *testing.T) {
	ui := drawerFixture()
	toolViewKey(ui, 'a')
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", FormattedResult: `{"result":"RESULT_MARKER"}`})
	view := ansi.Strip(ui.View().Content)
	if strings.Contains(view, "INPUT_MARKER") || !strings.Contains(view, "RESULT_MARKER") {
		t.Fatalf("completion changed independent drawer state: %s", view)
	}
	toolViewKey(ui, 'r')
	view = ansi.Strip(ui.View().Content)
	if strings.Contains(view, "INPUT_MARKER") || strings.Contains(view, "RESULT_MARKER") {
		t.Fatalf("result drawer did not close independently: %s", view)
	}
	toolViewKey(ui, 'a')
	view = ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "INPUT_MARKER") || strings.Contains(view, "RESULT_MARKER") {
		t.Fatalf("arguments drawer reopened result: %s", view)
	}
	toolViewKey(ui, tea.KeyEnter)
	toolViewKey(ui, tea.KeyEnter)
	view = ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "INPUT_MARKER") || strings.Contains(view, "RESULT_MARKER") {
		t.Fatal("overall disclosure reset section preferences")
	}
}

func TestToolReadableJSONSwitchIsSanitizedAndPresentationOnly(t *testing.T) {
	ui := drawerFixture()
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", FormattedResult: `{"result":"OUTPUT_MARKER","password":"RESULT_SECRET","control":"\u001b[2J\u202e"}`})
	before := ui.CanonicalThread().Entries()
	toolViewKey(ui, 'j')
	raw := ui.View().Content
	view := ansi.Strip(raw)
	for _, want := range []string{"view: json", `"path": "INPUT_MARKER"`, `"result": "OUTPUT_MARKER"`, `\necho second`, "[REDACTED]"} {
		if !strings.Contains(view, want) {
			t.Errorf("safe JSON view missing %q: %s", want, view)
		}
	}
	for _, secret := range []string{"SECRET_CANARY", "RESULT_SECRET", "\x1b[2J", "\u202e"} {
		if strings.Contains(raw, secret) {
			t.Errorf("JSON view exposed %q", secret)
		}
	}
	toolViewKey(ui, 'j')
	view = ansi.Strip(ui.View().Content)
	if strings.Contains(view, `"path"`) || !strings.Contains(view, "path: INPUT_MARKER") || !strings.Contains(view, "result: OUTPUT_MARKER") {
		t.Fatalf("readable view did not return: %s", view)
	}
	if !reflect.DeepEqual(before, ui.CanonicalThread().Entries()) {
		t.Fatal("view controls rewrote canonical call data")
	}
}

func TestNestedCallViewControlsDoNotAffectParent(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 100})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "parent", ToolID: "program", ToolName: "program", Parameters: map[string]any{"script": "PARENT_SCRIPT"}})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "child", ToolID: "inspect", ParentExecutionID: "parent", ParentToolID: "program", ToolName: "inspect", Parameters: map[string]any{"path": "CHILD_INPUT"}})
	expandFirstTool(ui)
	toolViewKey(ui, tea.KeyDown)
	toolViewKey(ui, tea.KeyEnter)
	toolViewKey(ui, 'a')
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "PARENT_SCRIPT") || strings.Contains(view, "CHILD_INPUT") {
		t.Fatalf("nested control did not target child: %s", view)
	}
	toolViewKey(ui, 'a')
	toolViewKey(ui, 'j')
	view = ansi.Strip(ui.View().Content)
	if !strings.Contains(view, `"path": "CHILD_INPUT"`) || !strings.Contains(view, "script:") {
		t.Fatalf("nested JSON switch affected parent representation: %s", view)
	}
}

func TestToolViewShortcutsDoNotEditTheComposer(t *testing.T) {
	ui := drawerFixture()
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "inspect", FormattedResult: `{"answer":"ok"}`})
	for _, key := range []rune{'a', 'r', 'j'} {
		toolViewKey(ui, key)
	}
	if ui.ComposerText() != "" {
		t.Fatalf("view shortcuts edited prompt: %q", ui.ComposerText())
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	for _, key := range []rune{'a', 'r', 'j'} {
		toolViewKey(ui, key)
	}
	if ui.ComposerText() != "arj" {
		t.Fatalf("normal composer typing intercepted: %q", ui.ComposerText())
	}
}
