package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestSteerTrayRendersQueuedMessages(t *testing.T) {
	m := New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})

	// Ensure no intro so key reaches the main composer handler.
	m.intro = nil

	// Simulate a live runner with queued messages.
	live := newQueueTestLive(t)
	live.QueueAppend("hello")
	live.QueueAppend("world")
	m.live = live
	m.session.Run.Running = true

	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'y'})
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"execution tray", "hello", "world", "2 queued message(s)", "prefer minimal diffs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("steer tray missing %q:\n%s", want, out)
		}
	}
}

func TestSteerTrayDeletesSelectedQueuedMessage(t *testing.T) {
	live := newQueueTestLive(t)
	live.QueueAppend("keep")
	live.QueueAppend("delete")
	tray := newSteerTray(live)

	tray.handleKey(tea.KeyPressMsg{Text: "j", Code: 'j'})
	tray.handleKey(tea.KeyPressMsg{Text: "d", Code: 'd'})

	snapshot := live.QueueSnapshot()
	if len(snapshot) != 1 || snapshot[0].Message.Content != "keep" {
		t.Fatalf("after selected delete: %+v", snapshot)
	}
}

func TestSteerTrayEnterEditsSelectedQueuedMessage(t *testing.T) {
	live := newQueueTestLive(t)
	live.QueueAppend("first")
	_, secondID := live.QueueAppend("second")
	tray := newSteerTray(live)

	tray.handleKey(tea.KeyPressMsg{Text: "j", Code: 'j'})
	a := tray.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	push, ok := a.(actionPush)
	if !ok {
		t.Fatalf("enter returned %T, want actionPush", a)
	}
	ed, ok := push.d.(*queueEditor)
	if !ok {
		t.Fatalf("pushed %T, want *queueEditor", push.d)
	}
	if ed.msgID != secondID || ed.text != "second" {
		t.Fatalf("editor = id %d text %q, want id %d text second", ed.msgID, ed.text, secondID)
	}

	ed.text = "edited second"
	ed.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	snapshot := live.QueueSnapshot()
	if len(snapshot) != 2 || snapshot[1].Message.Content != "edited second" {
		t.Fatalf("after edit: %+v", snapshot)
	}
}

func TestSteerTraySelectionAppendsControlOption(t *testing.T) {
	live := newQueueTestLive(t)
	tray := newSteerTray(live)

	tray.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	snapshot := live.QueueSnapshot()
	if len(snapshot) != 1 || snapshot[0].Message.Content != "stop after current tool" {
		t.Fatalf("control selection queued: %+v", snapshot)
	}
}

func TestSteerTrayQueuedInputIsDrainable(t *testing.T) {
	live := newQueueTestLive(t)
	live.QueueAppend("steer me")
	snapshot := live.QueueSnapshot()
	if len(snapshot) != 1 || snapshot[0].Message.Content != "steer me" {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	msgs := []llm.Message{}
	for msg := range live.DrainQueue(t.Context()) {
		msgs = append(msgs, msg)
	}
	if len(msgs) != 1 || msgs[0].Content != "steer me" {
		t.Fatalf("drained = %+v", msgs)
	}
	if len(live.QueueSnapshot()) != 0 {
		t.Fatal("queue not empty after Drain")
	}
}

func TestSteerTrayDepthGuard(t *testing.T) {
	m := New()
	m.SetRunFn(nil)
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})

	live := newQueueTestLive(t)
	live.QueueAppend("parent only")
	m.live = live

	// Submit while running; queued input should appear in tray.
	m.session.Run.Running = true
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'y'})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "parent only") {
		t.Fatalf("queued input not visible in tray:\n%s", out)
	}
}

func TestTimelineQueuedRowStillRendersWhileRunning(t *testing.T) {
	m := New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "run"})

	// Type while running → queued in timeline.
	m.session.Run.Running = true
	m.timeline.addQueuedUser("mid-run input")
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyTab}) // browse to see timeline
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "queued") || !strings.Contains(out, "mid-run input") {
		t.Fatalf("timeline missing queued row:\n%s", out)
	}
}
