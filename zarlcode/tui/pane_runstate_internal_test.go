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
	if hint := m.statusPane.statusHint(); !strings.Contains(hint, "esc stop") {
		t.Errorf("status hint should offer \"esc stop\" while running, got %q", hint)
	}

	m.session.Run.Running = false
	if got := m.session.headerMode(); got != "chat" {
		t.Errorf("headerMode = %q, want \"chat\" when idle", got)
	}
}
