package tui_test

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestAppliedModeUpdatesUIWithoutApproval(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(settlementProvider{}, ws, "fixture")
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "test modes"})
	live.SetPlanMode(true)
	applied := live.AppliedMode()
	applied.TaskID = "turn"
	applied.Reason = "investigate fixture"
	ui.Update(teasink.ModeChangedMsg(applied))
	assertComposerMode(t, ui, true)
	// A mode event must not open a modal that consumes subsequent composer input.
	ui.Update(textKey("continue"))
	if got := ui.ComposerText(); got != "continue" {
		t.Fatalf("mode change intercepted input: %q", got)
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assertComposerMode(t, ui, false)
	revision := ui.CanonicalThread().Revision()
	ui.Update(teasink.ModeChangedMsg(applied))
	assertComposerMode(t, ui, false)
	if ui.CanonicalThread().Revision() != revision {
		t.Fatal("stale mode event appended activity")
	}
}

func TestManualModeToggleReflectsReadOnlyCeiling(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(settlementProvider{}, ws, "fixture", engine.WithReadOnlyTasks())
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "test modes"})
	applied := live.AppliedMode()
	applied.TaskID = "turn"
	applied.Reason = "inspect-only task"
	ui.Update(teasink.ModeChangedMsg(applied))
	ui.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assertComposerMode(t, ui, true)
	if !live.AppliedMode().Plan {
		t.Fatalf("read-only ceiling lost: %+v", live.AppliedMode())
	}
}
