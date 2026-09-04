package tui_test

import (
	"os"
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
	live.RestoreContext([]llm.Message{{Role: "user", Content: "remember this"}})
	m.SetLiveRunner(live)
	m.SetSessionIdentity("session-id", "label", true, time.Now())
	typeAndEnter(t, m, "/clear")
	if len(live.ContextSnapshot()) != 0 {
		t.Fatal("history not cleared")
	}
	if m.SessionIdentity() != "" {
		t.Fatal("identity not cleared")
	}
	if !strings.Contains(m.View().Content, "conversation cleared") {
		t.Fatal("toast missing")
	}
}
func TestClearCancelsStartupSubmissionAndAttachments(t *testing.T) {
	root := t.TempDir()
	path := root + "/context.txt"
	if err := os.WriteFile(path, []byte("context"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := tui.New()
	m.SetLiveRunner(engine.NewLiveRunner(nil, ws, "test-model"))
	m.SetStartupReady(false)
	if err := m.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	m.Submit("later")
	m.Submit("/clear")
	if cmd := m.ApplyStartupReady(); cmd != nil {
		t.Fatal("clear left a startup turn to launch")
	}
	if m.StartupPrompt() != "" || !m.CanonicalThread().IsEmpty() {
		t.Fatalf("clear left pending submission: prompt=%q entries=%#v", m.StartupPrompt(), m.CanonicalThread().Entries())
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
