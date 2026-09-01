package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestSlashStatusHintShowsAndFiltersCommands(t *testing.T) {
	m := tui.New()
	step(t, m, window(120, 12))
	step(t, m, textKey("/"))
	got := m.View().Content
	for _, want := range []string{"/clear clear the conversation", "/help open key help"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %q", want, got)
		}
	}
	m = tui.New()
	step(t, m, window(120, 12))
	step(t, m, textKey("/cl"))
	got = m.View().Content
	if !strings.Contains(got, "/clear") || strings.Contains(got, "/help") {
		t.Fatalf("filter=%q", got)
	}
}
func TestSubmitSlashHelpOpensHelp(t *testing.T) {
	m := tui.New()
	step(t, m, window(120, 30))
	typeAndEnter(t, m, "/help")
	if !strings.Contains(m.View().Content, "key help") {
		t.Fatalf("help not open:\n%s", m.View().Content)
	}
}
func TestSubmitSlashClearClearsTimelineAndContext(t *testing.T) {
	m := tui.New()
	step(t, m, window(120, 30))
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(nil, ws, "")
	live.RestoreHistory([]llm.Message{{Role: "user", Content: "remember this"}})
	m.SetLiveRunner(live)
	m.SetSessionIdentity("session-id", "label", true, time.Now())
	typeAndEnter(t, m, "/clear")
	if len(live.History()) != 0 {
		t.Fatal("history not cleared")
	}
	if m.SessionIdentity() != "" {
		t.Fatal("identity not cleared")
	}
	if !strings.Contains(m.View().Content, "conversation cleared") {
		t.Fatal("toast missing")
	}
}
func TestSubmitUnknownSlashCommandDoesNotStartRun(t *testing.T) {
	m := tui.New()
	called := false
	m.SetRunFn(func(string) tea.Cmd { called = true; return nil })
	step(t, m, window(100, 12))
	typeAndEnter(t, m, "/wat")
	if called {
		t.Fatal("runner called")
	}
	if !strings.Contains(m.View().Content, "unknown slash command") {
		t.Fatal("toast missing")
	}
}
