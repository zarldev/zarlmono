package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// newQueueTestLive builds a LiveRunner over a throwaway workspace so the steer
// queue (seeded via submit) can be exercised through the exported API.
func newQueueTestLive(t *testing.T) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return engine.NewLiveRunner(nil, ws, nil, "")
}

func collectQueue(l *engine.LiveRunner, t *testing.T) []llm.Message {
	t.Helper()
	var out []llm.Message
	for msg := range l.DrainQueue(t.Context()) {
		out = append(out, msg)
	}
	return out
}

func TestSubmitWhileRunningQueuesAndRendersInjectedInput(t *testing.T) {
	m := New()
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = mm.(*UI)
	m.live = newQueueTestLive(t)

	m.handleRunnerMsg(teasink.ConversationStartedMsg{TaskID: "task", Depth: 0, Prompt: "start"})
	if cmd := m.submit("steer me next"); cmd != nil {
		t.Fatal("mid-run submit should queue input without launching a new command")
	}
	if got := m.live.QueueLen(); got != 1 {
		t.Fatalf("queued depth = %d, want 1", got)
	}
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "queued") || !strings.Contains(out, "steer me next") {
		t.Fatalf("queued input not visible in transcript:\n%s", out)
	}

	msgs := collectQueue(m.live, t)
	m.handleRunnerMsg(teasink.SteerInjectedMsg{TaskID: "task", Depth: 0, Messages: msgs})
	m.handleRunnerMsg(teasink.ContentMsg{TaskID: "task", Depth: 0, Delta: "ack"})

	out = ansi.Strip(m.View().Content)
	if strings.Contains(out, "sent") || !strings.Contains(out, "steer me next") || !strings.Contains(out, "ack") {
		t.Fatalf("injected input should render as plain user, not sent, with following response:\n%s", out)
	}
}

func TestQueuedInputPromotesToNextTurnWhenRunCompletesBeforeDrain(t *testing.T) {
	m := New()
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = mm.(*UI)
	m.live = newQueueTestLive(t)
	var launched string
	m.runFn = func(prompt string) tea.Cmd {
		launched = prompt
		return func() tea.Msg { return nil }
	}

	m.handleRunnerMsg(teasink.ConversationStartedMsg{TaskID: "task-1", Depth: 0, Prompt: "start"})
	m.submit("follow up")
	ok, cmd := m.handleRunnerMsg(teasink.ConversationEndedMsg{TaskID: "task-1", Depth: 0})
	if !ok || cmd == nil {
		t.Fatalf("completion should launch a queued follow-up turn, ok=%v cmd nil=%v", ok, cmd == nil)
	}
	if launched != "follow up" {
		t.Fatalf("launched prompt = %q, want queued follow-up", launched)
	}
	if got := m.live.QueueLen(); got != 0 {
		t.Fatalf("queue len after promoting follow-up = %d, want 0", got)
	}

	m.handleRunnerMsg(teasink.ConversationStartedMsg{TaskID: "task-2", Depth: 0, Prompt: "follow up"})
	out := ansi.Strip(m.View().Content)
	if count := strings.Count(out, "follow up"); count != 1 {
		t.Fatalf("queued follow-up should not be duplicated on next ConversationStarted; count=%d\n%s", count, out)
	}
}
