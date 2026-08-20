package tui

import (
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestUIInitSchedulesStartupWork(t *testing.T) {
	m := New()
	var called atomic.Bool
	m.startupCmd = func() tea.Msg {
		called.Store(true)
		return nil
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init command is nil")
	}
	if called.Load() {
		t.Fatal("Init ran startup work synchronously")
	}
	cmd()
	if !called.Load() {
		t.Fatal("scheduled startup command did not run")
	}
}

func TestUIAppliesDeferredStartupContextWindow(t *testing.T) {
	m := New()
	m.SetPressureConfig(engine.LiveContextWindow, 4096)
	m.SetLiveRunner(engine.NewLiveRunner(nil, code.Workspace{}, "local"))

	const window = 131072
	updated, _ := m.Update(startupReadyMsg{window: window})
	got := updated.(*UI)
	if got.live.RunTarget().Window != window {
		t.Fatalf("runner context window = %d, want %d", got.live.RunTarget().Window, window)
	}
	if got.session.Run.window != window {
		t.Fatalf("UI context window = %d, want %d", got.session.Run.window, window)
	}
	if got.session.Run.pressureWindow != window {
		t.Fatalf("pressure context window = %d, want %d", got.session.Run.pressureWindow, window)
	}
}

func TestFirstPromptWaitsForStartup(t *testing.T) {
	m := New()
	m.startupReady = false
	m.SetLiveRunner(engine.NewLiveRunner(nil, code.Workspace{}, "local"))
	if cmd := m.submit("hello"); cmd == nil {
		t.Fatal("submit should schedule toast expiry")
	}
	if m.startupPrompt != "hello" {
		t.Fatalf("queued startup prompt = %q", m.startupPrompt)
	}
	if m.session.Run.Running {
		t.Fatal("turn started before startup completed")
	}
}
