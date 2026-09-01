package tui_test

import (
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestUIInitSchedulesStartupWork(t *testing.T) {
	m := tui.New()
	var called atomic.Bool
	m.SetStartupCommand(func() tea.Msg { called.Store(true); return nil })
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init command is nil")
	}
	if called.Load() {
		t.Fatal("startup ran synchronously")
	}
	cmd()
	if !called.Load() {
		t.Fatal("scheduled startup did not run")
	}
}

func TestFirstPromptWaitsForRequiredStartup(t *testing.T) {
	m := tui.New()
	m.SetLiveRunner(newQueueLive(t))
	m.SetStartupReady(false)
	if m.Submit("hello") == nil {
		t.Fatal("submit should return toast command")
	}
	if got := m.StartupPrompt(); got != "hello" {
		t.Fatalf("startup prompt=%q", got)
	}
	m.SetStartupReady(true)
	m.Submit("now")
	if got := m.StartupPrompt(); got != "hello" {
		t.Fatalf("ready submit should not replace queued prompt: %q", got)
	}
}
