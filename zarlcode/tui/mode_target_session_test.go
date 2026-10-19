package tui_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

func TestClearRejectsQueuedInputWithoutChangingSession(t *testing.T) {
	fixture := newBeforeFixture(t)
	fixture.ui.SetStartupReady(true)
	before := fixture.ui.SessionIdentity()
	fixture.live.QueueAppend("belongs to the original conversation")
	fixture.ui.Submit("/clear")
	if fixture.ui.SessionIdentity() != before || fixture.live.QueueLen() != 1 {
		t.Fatal("clear changed identity or consumed queued input instead of rejecting")
	}
	fixture.live.QueueClear()
}

func TestFreshSessionRejectsStaleManualTargetCompletion(t *testing.T) {
	settings := newTestSettings(t)
	ui, live := newTargetUI(t, settings, prefs.ModelSelection{Provider: "llamacpp", Model: "original"})
	before := live.RunTarget()
	pending := ui.SwitchTarget(prefs.ModelSelection{Provider: "ollama", Model: "stale-manual"})
	if pending == nil {
		t.Fatal("manual provider preparation was not scheduled")
	}
	typeAndEnter(t, ui, "/clear")
	step(t, ui, pending())
	if got := live.RunTarget(); got.Spec != before.Spec || got.Model != before.Model {
		t.Fatalf("previous session's manual completion replaced fresh target: %+v", got)
	}
}
