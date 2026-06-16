package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestSlashStatusHint_ShowsCommands(t *testing.T) {
	got := slashStatusHint("/")
	for _, want := range []string{"/clear clear the conversation", "/help open key help"} {
		if !strings.Contains(got, want) {
			t.Fatalf("slash hint missing %q: %q", want, got)
		}
	}
}

func TestSlashStatusHint_FiltersByPrefix(t *testing.T) {
	got := slashStatusHint("/cl")
	if !strings.Contains(got, "/clear") || strings.Contains(got, "/help") {
		t.Fatalf("filtered slash hint = %q, want only /clear", got)
	}
}

func TestStatusHint_UsesSlashCommandsWhileTyping(t *testing.T) {
	m := New()
	m.composer.insert("/")
	got := m.statusHint()
	if !strings.Contains(got, "slash commands") || !strings.Contains(got, "/clear") {
		t.Fatalf("status hint = %q, want slash commands", got)
	}
}

func TestSubmitSlashHelpOpensHelp(t *testing.T) {
	m := New()
	cmd := m.submit("/help")
	if cmd != nil {
		t.Fatalf("/help cmd = %v, want nil", cmd)
	}
	if !m.overlay.active() {
		t.Fatal("/help should open help overlay")
	}
}

func TestSubmitSlashClearClearsTimelineAndContext(t *testing.T) {
	m := New()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(nil, ws, nil, "")
	live.RestoreHistory([]llm.Message{{Role: "user", Content: "remember this"}})
	m.SetLiveRunner(live)
	m.timeline.addUser("remember this")
	m.session.SetIdentity("session-id", "label", m.session.StartedAt)

	cmd := m.submit("/clear")
	if cmd == nil {
		t.Fatal("/clear should return toast/persistence command")
	}
	if got := live.History(); len(got) != 0 {
		t.Fatalf("history len = %d, want cleared", len(got))
	}
	if got := len(m.timeline.items); got != 0 {
		t.Fatalf("timeline items = %d, want cleared", got)
	}
	if m.session.ID != "" {
		t.Fatalf("session ID = %q, want cleared", m.session.ID)
	}
	if !strings.Contains(m.session.Toast, "conversation cleared") {
		t.Fatalf("toast = %q, want conversation cleared", m.session.Toast)
	}
}

func TestSubmitUnknownSlashCommandDoesNotStartRun(t *testing.T) {
	m := New()
	called := false
	m.runFn = func(string) tea.Cmd {
		called = true
		return nil
	}
	cmd := m.submit("/wat")
	if cmd == nil {
		// toast expiry command is expected so the status bar clears later.
		t.Fatal("unknown slash command should return toast expiry cmd")
	}
	if called {
		t.Fatal("unknown slash command should not dispatch to runner")
	}
	if !strings.Contains(m.session.Toast, "unknown slash command") {
		t.Fatalf("toast = %q, want unknown slash command", m.session.Toast)
	}
}
