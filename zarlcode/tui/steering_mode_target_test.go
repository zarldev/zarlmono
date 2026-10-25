package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

func TestActiveTurnAcceptsSteeringAfterModeTargetDeferred(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetStartupReady(true)
	step(t, f.ui, window(120, 40))
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	setPlanRoute(t, settings, "ollama", "next-plan-model")
	f.ui.SetSettings(settings)
	active := f.live.RunTarget().Spec

	started := make(chan context.Context, 1)
	release := make(chan struct{})
	f.provider.check = func(ctx context.Context) {
		started <- ctx
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	cmd := f.ui.Submit("keep working")
	if cmd == nil {
		t.Fatal("missing initial dispatch")
	}
	done := make(chan struct{})
	go func() { defer close(done); cmd() }()
	t.Cleanup(func() { f.live.CancelTurn(); <-done })
	turnCtx := <-started
	f.applyEvents()

	f.ui.SetPlanMode(true)
	if _, cmd := f.ui.Update(nil); cmd != nil {
		t.Fatal("target preparation started during the active turn")
	}
	if view := f.ui.View().Content; strings.Contains(view, "mode target is not ready") {
		t.Fatal("deferred mode change is presented as a readiness error")
	}
	f.ui.Submit("check the existing implementation first")
	f.ui.Update(tea.PasteMsg{Content: "keep the change small"})
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	queued := f.live.QueueSnapshot()
	want := []string{"check the existing implementation first", "keep the change small"}
	if len(queued) != len(want) {
		t.Fatalf("queued %d steering messages, want %d; toast: %s", len(queued), len(want), f.ui.ToastText())
	}
	for i, text := range want {
		if queued[i].Message.Content != text {
			t.Errorf("queued message %d = %q, want %q", i, queued[i].Message.Content, text)
		}
	}
	if text := f.ui.ComposerText(); text != "" {
		t.Errorf("accepted steering remained in composer: %q", text)
	}
	if err := turnCtx.Err(); err != nil {
		t.Fatalf("steering interrupted the active turn: %v", err)
	}
	if got := f.live.RunTarget().Spec; got != active {
		t.Fatalf("steering changed the active provider target: got %+v, want %+v", got, active)
	}
	close(release)
	<-done
}

func TestNewTurnStillWaitsForModeTarget(t *testing.T) {
	for _, state := range []string{"preparing", "failed"} {
		t.Run(state, func(t *testing.T) {
			f := newBeforeFixture(t)
			f.ui.SetStartupReady(true)
			step(t, f.ui, window(120, 40))
			settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
			setPlanRoute(t, settings, "not-a-provider", "unavailable-model")
			f.ui.SetSettings(settings)
			active := f.live.RunTarget().Spec
			prepare := schedulePlanTarget(t, f.ui)
			if view := f.ui.View().Content; strings.Contains(view, "mode target is not ready") {
				t.Fatal("target preparation is presented as a readiness error")
			}
			if state == "failed" {
				step(t, f.ui, prepare())
			}

			const prompt = "start a new turn"
			f.ui.Update(tea.PasteMsg{Content: prompt})
			f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if got := f.ui.ComposerText(); got != prompt {
				t.Fatalf("new-turn draft = %q, want retained %q", got, prompt)
			}
			if got := f.live.QueueLen(); got != 0 {
				t.Fatalf("new turn was queued as steering: %d", got)
			}
			if got := f.live.RunTarget().Spec; got != active {
				t.Fatalf("blocked submission changed target: got %+v, want %+v", got, active)
			}
			toast := f.ui.ToastText()
			if state == "preparing" {
				if !strings.Contains(toast, "preparing Plan target") || strings.HasPrefix(toast, "✗") {
					t.Fatalf("pending target should be informational: %s", toast)
				}
				if strings.Contains(f.ui.View().Content, "mode target is not ready") {
					t.Fatal("pending target still renders as a readiness error")
				}
			} else if !strings.Contains(toast, "mode target: prepare Plan target") || !strings.HasPrefix(toast, "✗") {
				t.Fatalf("missing genuine target failure: %s", toast)
			}
		})
	}
}
