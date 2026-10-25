package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestExpandedInputsRemainThroughWorkspaceWaitAndFailure(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 70})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "read", Parameters: map[string]any{"path": "source.go", "extra": "argument-stays-visible"}})
	expandFirstTool(ui)
	ui.Update(teasink.WorkspaceWaitStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "read", Access: tools.WorkspaceAccesses.READ, Paths: []string{"source.go"}})
	if view := ansi.Strip(ui.View().Content); !strings.Contains(view, "waiting") || !strings.Contains(view, "argument-stays-visible") {
		t.Fatalf("waiting call hid its arguments:\n%s", view)
	}
	ui.Update(teasink.WorkspaceWaitEndedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call"})
	ui.Update(teasink.ToolFailedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "read", Error: "synthetic failure"})
	if view := ansi.Strip(ui.View().Content); !strings.Contains(view, "argument-stays-visible") || !strings.Contains(view, "synthetic failure") {
		t.Fatalf("failure hid input or diagnostic:\n%s", view)
	}
}

func TestSubagentToolInputDoesNotReplaceRootInput(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 70})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Prompt: "inspect"})
	ui.Update(teasink.ToolStartedMsg{TaskID: "root", ExecutionID: "parent", ToolID: "shared-id", ToolName: "agent_spawn", Parameters: map[string]any{"agent": "reviewer", "prompt": "private child task"}})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, ParentExecutionID: "parent", ParentToolCallID: "shared-id", AgentName: "reviewer", Prompt: "private child task"})
	ui.Update(teasink.ToolStartedMsg{TaskID: "child", Depth: 1, ExecutionID: "child-call", ToolID: "shared-id", ToolName: "read", Parameters: map[string]any{"path": "child.go", "extra": "child-argument-marker"}})
	inputs := map[string]string{}
	for _, entry := range ui.CanonicalThread().Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYTOOLCALL {
			inputs[entry.Payload.ExecutionID] = entry.Payload.ToolInput
		}
	}
	if !strings.Contains(inputs["parent"], "private child task") || strings.Contains(inputs["parent"], "child-argument-marker") || !strings.Contains(inputs["child-call"], "child-argument-marker") {
		t.Fatalf("tool input ownership crossed execution identities: %+v", inputs)
	}
}
