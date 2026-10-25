package tui_test

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRunningBashCommandUsesRealLinesNotJSONEscapes(t *testing.T) {
	command := "echo first-line\nfor item in one two; do\n  echo \"$item\"\ndone\necho LAST_COMMAND_LINE"
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 90})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "bash", Parameters: map[string]any{"command": command, "background": false, "timeout_seconds": 90, "description": "synthetic demonstration"}})
	expandFirstTool(ui)
	view := ansi.Strip(ui.View().Content)
	for _, want := range []string{"command:", "echo first-line", "for item in one two; do", `echo "$item"`, "LAST_COMMAND_LINE", "background: false", "timeout_seconds: 90"} {
		if !strings.Contains(view, want) {
			t.Fatalf("readable command missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, `"command"`) || strings.Contains(view, `\nfor item`) {
		t.Fatalf("command is still a JSON transport dump:\n%s", view)
	}
	var snapshot string
	for _, entry := range ui.CanonicalThread().Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYTOOLCALL {
			snapshot = entry.Payload.ToolInput
		}
	}
	var stored map[string]any
	if json.Unmarshal([]byte(snapshot), &stored) != nil || stored["command"] != command {
		t.Fatal("readable rendering changed the canonical call snapshot")
	}
}

func TestRunningPlanCallUsesStatusListAndKeepsExtraFields(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 90})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "plan", ToolName: "update_plan", Parameters: map[string]any{
		"explanation": "Verify the request before completion",
		"plan":        []any{map[string]any{"step": "Inspect the source", "status": "completed"}, map[string]any{"step": "Render readable calls", "status": "in_progress"}, map[string]any{"step": "Verify the result", "status": "pending"}},
		"extra":       "preserved-extra-field",
	}})
	expandFirstTool(ui)
	view := ansi.Strip(ui.View().Content)
	for _, want := range []string{"✓", "▶", "☐", "Inspect the source", "Render readable calls", "Verify the result", "preserved-extra-field"} {
		if !strings.Contains(view, want) {
			t.Fatalf("readable plan missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, `"status"`) || strings.Contains(view, `"step"`) {
		t.Fatalf("plan still rendered as JSON:\n%s", view)
	}
}

func TestGenericJSONResultsUseReadableFieldsWithoutUnsafeControlDecoding(t *testing.T) {
	out := tui.RenderContent(100, tui.Content{Kind: tui.ContentToolResult, ToolName: "mcp_call", Text: `{"count":9007199254740993,"enabled":false,"details":{"message":"line one\nline two\u001b[2J\u202e"},"items":["first","last"],"api_key":"JSON_SECRET_CANARY"}`})
	raw := strings.Join(out, "\n")
	if strings.Contains(raw, "\x1b[2J") || strings.Contains(raw, "\u202e") {
		t.Fatal("JSON decoding activated terminal or bidi controls")
	}
	view := ansi.Strip(strings.Join(out, "\n"))
	for _, want := range []string{"count: 9007199254740993", "enabled: false", "details:", "line one", "line two", "first", "last", "[REDACTED]"} {
		if !strings.Contains(view, want) {
			t.Errorf("readable result missing %q: %s", want, view)
		}
	}
	for _, unsafe := range []string{"JSON_SECRET_CANARY", "\x1b[2J", "\u202e", `"details"`} {
		if strings.Contains(view, unsafe) {
			t.Errorf("result exposed unsafe or raw JSON content %q", unsafe)
		}
	}
}

func TestJSONSourceFilesKeepTheirCodeRepresentation(t *testing.T) {
	out := tui.RenderContent(100, tui.Content{Kind: tui.ContentToolResult, ToolName: "read", Hint: "config.json", Text: `{"enabled":false}`})
	view := ansi.Strip(strings.Join(out, "\n"))
	if !strings.Contains(view, `"enabled"`) {
		t.Fatalf("JSON source file was reinterpreted as a tool record: %s", view)
	}
}

func TestTypedPlanResultsUseExistingPlanPresentation(t *testing.T) {
	plan := code.Plan{Steps: []code.PlanStep{{Text: "Completed task", Status: code.StepStatuses.COMPLETED}, {Text: "Pending task", Status: code.StepStatuses.PENDING}}}
	out := ansi.Strip(strings.Join(tui.RenderTypedToolResult(100, "update_plan", "", plan), "\n"))
	if !strings.Contains(out, "✓") || !strings.Contains(out, "☐") || strings.Contains(out, `"steps"`) {
		t.Fatalf("typed plan did not use status presentation: %s", out)
	}
}

func TestPlanWithUnknownStepFieldsFallsBackWithoutDroppingData(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 80})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "plan", ToolName: "update_plan", Parameters: map[string]any{"plan": []any{map[string]any{"step": "Inspect", "status": "unsupported-status", "extra": "KEEP_UNKNOWN_FIELD"}}}})
	expandFirstTool(ui)
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "KEEP_UNKNOWN_FIELD") || !strings.Contains(view, "unsupported-status") {
		t.Fatalf("specialized plan view discarded unknown input: %s", view)
	}
}

func TestReadableToolFieldsRetainDeepValuesAtNarrowWidth(t *testing.T) {
	out := tui.RenderContent(28, tui.Content{Kind: tui.ContentToolResult, ToolName: "mcp_call", Text: `{"fields":{"one":{"two":{"three":{"four":{"five":{"leaf":"DEEP_VALUE_TAIL"}}}}}},"empty_list":[],"empty_object":{},"nothing":null}`})
	joined := ansi.Strip(strings.Join(out, "\n"))
	words := strings.Join(strings.Fields(joined), " ")
	for _, want := range []string{"(empty list)", "(empty object)", "nothing: null"} {
		if !strings.Contains(words, want) {
			t.Errorf("lost readable value %q: %s", want, joined)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(joined), ""), "DEEP_VALUE_TAIL") {
		t.Fatalf("depth/width handling lost a value: %s", joined)
	}
}
