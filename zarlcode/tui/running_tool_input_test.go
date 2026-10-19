package tui_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func expandFirstTool(ui *tui.UI) {
	for _, key := range []rune{tea.KeyTab, tea.KeyEnter, tea.KeyDown, tea.KeyEnter} {
		ui.Update(tea.KeyPressMsg{Code: key})
	}
}

func TestExpandedRunningToolShowsAllArgumentsBeforeCompletion(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 130})
	arguments := make(map[string]any)
	for i := range 55 {
		arguments[fmt.Sprintf("field_%02d", i)] = fmt.Sprintf("argument-value-%02d", i)
	}
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "inspect", Parameters: arguments})
	if strings.Contains(ansi.Strip(ui.View().Content), "argument-value-54") {
		t.Fatal("running call expanded without user action")
	}
	expandFirstTool(ui)
	view := ansi.Strip(ui.View().Content)
	for _, want := range []string{"arguments:", "argument-value-00", "argument-value-54", "running"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded running call missing %q:\n%s", want, view)
		}
	}
	ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "inspect", FormattedResult: "finished-output"})
	view = ansi.Strip(ui.View().Content)
	for _, want := range []string{"arguments:", "argument-value-54", "result:", "finished-output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("completed call lost %q:\n%s", want, view)
		}
	}
}

func TestToolInputsRedactedBeforeDisplayOrPersistence(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, raw := range []bool{false, true} {
		ui := tui.New()
		ui.Update(tea.WindowSizeMsg{Width: 140, Height: 80})
		parameters := map[string]any{"command": "TOKEN=\"embedded-secret-canary\" printf safe", "api_key": "structured-key-canary", "env": map[string]any{"OPENAI_API_KEY": "environment-key-canary"}, "headers": map[string]any{"Authorization": "Bearer bearer-canary-value"}, "script": "curl -H 'Authorization: Basic basic-auth-canary' https://user:url-password-canary@example.invalid", "count": json.Number("9007199254740993"), "path": "safe.go\x1b[2J\u202e"}
		before, _ := json.Marshal(parameters)
		event := teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "bash", Parameters: parameters}
		if raw {
			event.RawArguments = string(before)
		}
		ui.Update(event)
		expandFirstTool(ui)
		view := ansi.Strip(ui.View().Content)
		var input string
		for _, entry := range ui.CanonicalThread().Entries() {
			if entry.Kind == transcript.EntryKinds.ENTRYTOOLCALL {
				input = entry.Payload.ToolInput
			}
		}
		if input == "" || !strings.Contains(input, "9007199254740993") {
			t.Fatal("full call lost input or integer precision")
		}
		for _, secret := range []string{"embedded-secret-canary", "structured-key-canary", "environment-key-canary", "bearer-canary-value", "basic-auth-canary", "url-password-canary", "\x1b[2J", "\u202e"} {
			if strings.Contains(input, secret) || strings.Contains(view, secret) {
				t.Fatalf("unsafe input survived display snapshot: %q", secret)
			}
		}
		if !strings.Contains(input, "[REDACTED]") {
			t.Fatal("redaction not visible")
		}
		after, _ := json.Marshal(parameters)
		if string(before) != string(after) {
			t.Fatal("presentation mutated execution arguments")
		}
	}
}

func TestExpandedProgramInputsDoNotDisplaceNestedToolToggles(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 90})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "parent", ToolID: "program", ToolName: "program", Parameters: map[string]any{"script": "emit(call(\"read\", {\"path\": \"child.go\"}))"}})
	ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "child", ToolID: "read", ParentExecutionID: "parent", ParentToolID: "program", ToolName: "read", Parameters: map[string]any{"path": "child.go", "extra": "nested-argument-tail"}})
	expandFirstTool(ui)
	if strings.Contains(ansi.Strip(ui.View().Content), "nested-argument-tail") {
		t.Fatal("child call opened with its parent")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if view := ansi.Strip(ui.View().Content); !strings.Contains(view, "nested-argument-tail") {
		t.Fatalf("request rows displaced child keyboard selection:\n%s", view)
	}
}

func TestToolInputsSurviveCompletionAndResume(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetSessionIdentity("tool-inputs", "Tool inputs", false, time.Now())
	f.ui.Update(tea.WindowSizeMsg{Width: 140, Height: 70})
	f.ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "read", Parameters: map[string]any{"path": "source.go", "limit": 123, "extra": "saved-argument-tail"}})
	f.ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "call", ToolName: "read", FormattedResult: "completed"})
	f.live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "inspect"}})
	if err := f.ui.SaveSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	resumed := tui.New()
	resumed.SetLiveRunner(f.live)
	resumed.SetSettings(settings)
	if err := resumed.ResumeSavedSession(t.Context(), "tool-inputs"); err != nil {
		t.Fatal(err)
	}
	resumed.Update(tea.WindowSizeMsg{Width: 140, Height: 70})
	if !reflect.DeepEqual(resumed.CanonicalThread().Entries(), f.ui.CanonicalThread().Entries()) {
		t.Fatal("resume changed call arguments")
	}
	expandFirstTool(resumed)
	if view := ansi.Strip(resumed.View().Content); !strings.Contains(view, "saved-argument-tail") {
		t.Fatalf("restored call missing submitted arguments:\n%s", view)
	}
}
