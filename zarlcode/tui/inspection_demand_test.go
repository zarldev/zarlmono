package tui_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestInspectionDemandRetainsFirstForegroundRequest(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetStartupReady(true)
	var calls atomic.Int32
	f.provider.check = func(context.Context) { calls.Add(1) }
	release := holdInspectionAdmission(t, f, true)
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	f.ui.Update(tea.PasteMsg{Content: "first prompt"})
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = f.ui.LaunchQueuedTurnForTest()
	if !strings.Contains(f.ui.ToastText(), "previous foreground input retained") {
		t.Fatalf("first demand was replaced: %s", f.ui.ToastText())
	}
	cmd := release()
	if cmd == nil {
		t.Fatal("first foreground request was not resumed")
	}
	driveBeforeCommand(f, cmd)
	if calls.Load() != 1 || f.ui.ComposerText() != "" {
		t.Fatalf("first prompt not dispatched: calls=%d draft=%q", calls.Load(), f.ui.ComposerText())
	}
	f.applyEvents()
}

func TestInspectionDemandDoesNotDispatchAfterSessionChange(t *testing.T) {
	for _, stale := range []string{"session", "generation"} {
		t.Run(stale, func(t *testing.T) {
			f := newBeforeFixture(t)
			f.ui.SetStartupReady(true)
			var calls atomic.Int32
			f.provider.check = func(context.Context) { calls.Add(1) }
			release := holdInspectionAdmission(t, f, true)
			f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			f.ui.Update(tea.PasteMsg{Content: "old session prompt"})
			f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if stale == "session" {
				f.ui.SetSessionIdentity("different-session", "different", false, time.Now())
			} else {
				tui.AdvanceInspectionGenerationForTest(f.ui)
			}
			if cmd := release(); cmd != nil {
				t.Fatal("stale demand returned a dispatch command")
			}
			if calls.Load() != 0 || !strings.Contains(f.ui.ToastText(), "not dispatched") {
				t.Fatalf("stale request was not reported: calls=%d toast=%s", calls.Load(), f.ui.ToastText())
			}
		})
	}
}

func TestInspectionAdmissionCleanupCancelsWorker(t *testing.T) {
	f := newBeforeFixture(t)
	_ = holdInspectionAdmission(t, f, true) // cleanup cancels and joins without an explicit release
}
