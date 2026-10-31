package tui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func renameVisibleSession(ui *tui.UI, name string) {
	ui.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	for range 80 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range name {
		ui.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestSessionIdentitySharedByHeaderAndTerminal(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	assertName := func(want string) {
		t.Helper()
		view := ui.View()
		if view.WindowTitle != want+" — ƶarl/code" {
			t.Fatalf("terminal title=%q", view.WindowTitle)
		}
		header, _, _ := strings.Cut(ansi.Strip(view.Content), "\n")
		if !strings.Contains(header, want) {
			t.Fatalf("header missing shared name %q: %q", want, header)
		}
	}
	assertName("New session")
	renameVisibleSession(ui, "Fix queued colours")
	assertName("Fix queued colours")
	ui.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	ui.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	assertName("Fix queued colours")
	ui.Update(teasink.ConversationStartedMsg{TaskID: "named", Prompt: "Different first prompt"})
	assertName("Fix queued colours")
	renameVisibleSession(ui, "")
	assertName("New session")
	ui.Update(teasink.ConversationEndedMsg{TaskID: "named", Reason: runner.TerminalCompleted})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "auto", Prompt: "Automatic name"})
	assertName("New session") // Explicitly cleared names stay cleared.
	ui = tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	ui.SetLiveRunner(newQueueLive(t))
	ui.Submit("Automatic name") // The returned run command is not executed.
	assertName("Automatic name")
}

func TestRestoredSessionNameIsSafeAndWidthBounded(t *testing.T) {
	root := t.TempDir()
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	label := "Review\x1b[31m 界\x1b[0m\x07\n\u202e" + strings.Repeat("long name ", 12)
	if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{ID: "named", Workspace: root, Label: label, PendingJSON: []byte(`{"text":"draft"}`)}); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, root))
	if err := ui.ResumeSavedSession(t.Context(), "named"); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{20, 32, 60, 120, 220} {
		ui.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		view := ui.View()
		if strings.ContainsAny(view.WindowTitle, "\x1b\x07\n\u202e") || !strings.HasPrefix(view.WindowTitle, "Review 界 ") {
			t.Fatalf("unsafe or lost restored title: %q", view.WindowTitle)
		}
		header, _, _ := strings.Cut(ansi.Strip(view.Content), "\n")
		if ansi.StringWidth(header) > width || !strings.Contains(header, "Review") {
			t.Fatalf("header identity lost at width %d: %q", width, header)
		}
		if strings.Contains(header, "follow") {
			t.Fatalf("normal following should leave a quiet header at width %d: %q", width, header)
		}
	}
}

func TestInputActivityOwnsTurnTimerWithoutResetOnChildEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := tui.New()
		ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		ui.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "Named task"})
		check := func(activity, timer string) {
			t.Helper()
			canvas := surfaceCanvas(ui, 100, 30)
			line := transcriptScreenRow(canvas, 27)
			if canvas.CellAt(0, 27).Content != "─" || canvas.CellAt(1, 27).Content != " " || canvas.CellAt(2, 27).Content == "─" || canvas.CellAt(99, 27).Content != "─" {
				t.Fatal("activity must start at the left text inset, leaving the right separator clear")
			}
			if !strings.Contains(line, activity) || !strings.Contains(line, timer) {
				t.Fatalf("input activity missing %s/%s: %q", activity, timer, line)
			}
			if strings.Contains(transcriptScreenRow(canvas, 0), timer) {
				t.Fatal("activity duplicated in header")
			}
		}
		check("working", "0:00")
		time.Sleep(65 * time.Second)
		ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ToolID: "call", ToolName: "read"})
		check("running read", "1:05")
		ui.Update(teasink.ConversationStartedMsg{TaskID: "child", Depth: 1})
		time.Sleep(2 * time.Second)
		check("running read", "1:07")
		ui.SetErrorToast("persistent warning")
		check("running read", "1:07")
		if !strings.Contains(transcriptScreenRow(surfaceCanvas(ui, 100, 30), 29), "persistent warning") {
			t.Fatal("activity obscured feedback")
		}
		ui.Update(tea.WindowSizeMsg{Width: 20, Height: 30})
		line := transcriptScreenRow(surfaceCanvas(ui, 20, 30), 27)
		if !strings.Contains(line, "1:07") || ansi.StringWidth(line) > 20 {
			t.Fatalf("narrow activity lost timer: %q", line)
		}
		ui.Update(teasink.ConversationEndedMsg{TaskID: "turn", Reason: runner.TerminalCompleted})
		if line = transcriptScreenRow(surfaceCanvas(ui, 20, 30), 27); line != strings.Repeat("─", 20) {
			t.Fatalf("completed input separator not quiet: %q", line)
		}
		ui.Update(teasink.ConversationStartedMsg{TaskID: "next", Prompt: "next"})
		if !strings.Contains(transcriptScreenRow(surfaceCanvas(ui, 20, 30), 27), "0:00") {
			t.Fatal("new turn did not reset timer")
		}
	})
}

func TestSessionHeaderKeepsViewportWithJoinedEmoji(t *testing.T) {
	for _, grapheme := range []bool{false, true} {
		ui := tui.New()
		if grapheme {
			ui.Update(tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeSet})
		}
		ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		renameVisibleSession(ui, "Review "+strings.Repeat("👨‍👩‍👦", 12))
		ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "browse this"}})
		_ = ui.View()
		ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		for _, width := range []int{32, 60} {
			ui.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			header, _, _ := strings.Cut(ansi.Strip(ui.View().Content), "\n")
			if !strings.Contains(header, "Review") || !strings.Contains(header, "browse 100%") {
				t.Fatalf("grapheme=%t width=%d clipped identity/viewport: %q", grapheme, width, header)
			}
		}
	}
}

func TestPublicUIRetainsMixedCaseModelAndToolWithLiveMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := tui.New()
		ui.SetWorkspace(t.TempDir(), "Claude-Opus4.6")
		ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		ui.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "work"})
		time.Sleep(65 * time.Second)
		usage := &llm.Usage{CompletionTokens: 23 * 65}
		ui.Update(teasink.IterationCompletedMsg{TaskID: "turn", Iter: 1, Usage: usage, Delta: usage})
		ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ToolID: "call", ToolName: "ReadFile"})
		canvas := surfaceCanvas(ui, 100, 30)
		header := transcriptScreenRow(canvas, 0)
		activity := transcriptScreenRow(canvas, 27)
		if !strings.Contains(header, "Claude-Opus4.6") {
			t.Fatalf("model identifier lost casing: %q", header)
		}
		for _, want := range []string{"running ReadFile", "23 tok/s", "1:05"} {
			if !strings.Contains(activity, want) {
				t.Fatalf("activity lost %q: %q", want, activity)
			}
		}
		if !strings.ContainsAny(activity, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
			t.Fatalf("activity spinner lost: %q", activity)
		}
	})
}
