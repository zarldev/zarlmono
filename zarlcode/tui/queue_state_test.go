package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func newQueueLive(t *testing.T) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return engine.NewLiveRunner(nil, ws, "")
}

func TestSubmitWhileRunningQueuesAndRendersInjectedInput(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 200, Height: 50})
	live := newQueueLive(t)
	m.SetLiveRunner(live)
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "task", Prompt: "start"})
	m.Submit("steer me next")
	if live.QueueLen() != 1 {
		t.Fatalf("queue len=%d", live.QueueLen())
	}
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "queued") || !strings.Contains(out, "steer me next") {
		t.Fatalf("queued input absent:\n%s", out)
	}
}

func TestSteerTrayRendersEditsDeletesAndQueuesControls(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	live := newQueueLive(t)
	live.QueueAppend("hello")
	live.QueueAppend("world")
	m.SetLiveRunner(live)
	m.SetRunning(true)
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'y'})
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"live controls", "hello", "world", "2 queued · 5 controls", "prefer minimal di"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tray missing %q:\n%s", want, out)
		}
	}
	stepUI(t, m, tea.KeyPressMsg{Text: "j", Code: 'j'})
	stepUI(t, m, tea.KeyPressMsg{Text: "d", Code: 'd'})
	q := live.QueueSnapshot()
	if len(q) != 1 || q[0].Message.Content != "hello" {
		t.Fatalf("delete result=%+v", q)
	}
}
