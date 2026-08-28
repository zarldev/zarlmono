package tui

import (
	"strings"
	"testing"
)

// The header/status panes read run state through Session.Run, the single owner
// of UI-visible run telemetry. Runner events mutate that same Session field, so
// panes must observe live-turn transitions without pointer aliasing back to UI.
func TestSessionRunOwnsLiveRunState(t *testing.T) {
	m := New()

	m.session.Run.Running = true
	if got := m.session.headerMode(); got != "build" {
		t.Errorf("headerMode = %q, want \"build\" while a turn is live", got)
	}
	if hint := m.statusPane.statusHint(); !strings.Contains(hint, "build mode") || strings.Contains(hint, "running") || strings.Contains(hint, "ctrl+") {
		t.Errorf("status should keep workflow context without duplicating live state, got %q", hint)
	}

	m.session.Run.Running = false
	if got := m.session.headerMode(); got != "chat" {
		t.Errorf("headerMode = %q, want \"chat\" when idle", got)
	}
}

func TestLiveTurnFinishedReconcilesMissingTerminalEvent(t *testing.T) {
	m := New()
	m.session.Run.Running = true
	m.session.Run.activeTopLevel = "task-1"

	stepUI(t, m, liveTurnFinishedMsg{})

	if m.session.Run.Running || m.session.Run.activeTopLevel != "" {
		t.Fatalf("finished command left stale run: running=%t task=%q", m.session.Run.Running, m.session.Run.activeTopLevel)
	}
}
