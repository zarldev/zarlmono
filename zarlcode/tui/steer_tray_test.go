package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSteerTraySelectionAppendsControlOption(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	live := newQueueLive(t)
	m.SetLiveRunner(live)
	m.SetRunning(true)
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'y'})
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	q := live.QueueSnapshot()
	if len(q) != 1 || q[0].Message.Content != "stop after current tool" {
		t.Fatalf("control selection=%+v", q)
	}
}
