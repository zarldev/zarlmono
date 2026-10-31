package tui_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

// The queued case starts with an already queued input: ReserveRuntime cannot
// admit an inspector over a nonempty queue, so only the worker drain is held.
func holdInspectionAdmission(t *testing.T, f *beforeFixture, holdRuntime bool) func() tea.Cmd {
	t.Helper()
	f.ui.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	f.ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan tea.Msg, 1)
	var once sync.Once
	var delivered bool
	var next tea.Cmd
	drain := func() tea.Cmd {
		t.Helper()
		once.Do(func() {
			close(release)
			select {
			case msg := <-result:
				_, next = f.ui.Update(msg)
				delivered = true
			case <-time.After(5 * time.Second):
				t.Error("inspection did not drain")
			}
		})
		if !delivered {
			return nil
		}
		return next
	}
	t.Cleanup(func() {
		tui.CancelInspectionForTest(f.ui)
		_ = drain()
	})
	cmd := tui.InspectionCommandForTest(f.ui, func(ctx context.Context) tui.InspectorSnapshot {
		if holdRuntime {
			admission, err := f.live.ReserveRuntime()
			if err != nil {
				t.Errorf("inspection admission: %v", err)
				close(started)
				return tui.InspectorSnapshot{}
			}
			close(started)
			<-ctx.Done()
			<-release
			admission.Release()
		} else {
			close(started)
			<-ctx.Done()
			<-release
		}
		return tui.InspectorSnapshot{}
	})
	go func() { result <- cmd() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		_ = drain()
		t.Fatal("inspection not admitted")
	}
	if holdRuntime {
		if reservation, err := f.live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
			if reservation != nil {
				reservation.Release()
			}
			_ = drain()
			t.Fatalf("inspection did not hold runtime: %v", err)
		}
	}
	return drain
}

func TestInspectionForegroundAdmissionWaitsForDrain(t *testing.T) {
	for _, kind := range []string{"submit", "queue", "swarm"} {
		t.Run(kind, func(t *testing.T) {
			f := newBeforeFixture(t)
			f.ui.SetStartupReady(true)
			var calls atomic.Int32
			f.provider.check = func(ctx context.Context) {
				calls.Add(1)
				active, err := f.store.GetSettingExact(ctx, f.ws.Root(), "active_session")
				if err != nil {
					t.Error(err)
					return
				}
				checkpoints, err := f.store.ListSessionCheckpoints(ctx, active)
				if err != nil || len(checkpoints) == 0 {
					t.Errorf("BEFORE not durable at provider dispatch: %v %v", checkpoints, err)
					return
				}
				record, err := f.store.GetSessionCheckpoint(ctx, active, checkpoints[len(checkpoints)-1].ID)
				if err != nil {
					t.Error(err)
					return
				}
				checkpoint, err := rewind.Load(ctx, f.store, record)
				if err != nil {
					t.Error(err)
					return
				}
				snapshot, err := checkpoint.Snapshot()
				if err != nil || snapshot.Boundary.PromptText == "" {
					t.Errorf("missing BEFORE prompt: %v", err)
				}
			}
			if kind != "submit" {
				settleRewindTurn(t, f, "seed exact session")
			}
			if kind == "queue" {
				if _, id := f.live.QueueAppend("queued foreground"); id == 0 {
					t.Fatal("could not seed queued input")
				}
			}
			release := holdInspectionAdmission(t, f, kind != "queue")
			released := false
			defer func() {
				if !released {
					_ = release()
				}
			}()
			switch kind {
			case "submit":
				f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
				f.ui.Update(tea.PasteMsg{Content: "foreground prompt"})
				f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			case "queue":
				if cmd := f.ui.LaunchQueuedTurnForTest(); cmd != nil {
					t.Fatal("queued input dispatched before inspection drained")
				}
			case "swarm":
				f.ui.StartSwarm("foreground assignment")
			}
			if !strings.Contains(f.ui.ToastText(), "waiting for inspection") {
				t.Fatalf("foreground was not deferred: %s", f.ui.ToastText())
			}
			wantCalls := int32(0)
			if kind != "submit" {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("provider called while inspector holds runtime: %d", calls.Load())
			}
			cmd := release()
			released = true
			if kind == "swarm" {
				if !strings.Contains(f.ui.ToastText(), "swarm started") {
					t.Fatalf("swarm did not start: %s", f.ui.ToastText())
				}
				stopped := tui.SwarmStoppedForTest(f.ui)
				f.ui.StopSwarm()
				select {
				case <-stopped:
				case <-time.After(5 * time.Second):
					t.Fatal("swarm did not stop")
				}
				f.applyEvents()
				if !strings.Contains(f.ui.ToastText(), "swarm stopped") {
					t.Fatalf("swarm did not report stop: %s", f.ui.ToastText())
				}
				return
			}
			if cmd == nil {
				t.Fatal("inspection drain did not resume foreground dispatch")
			}
			driveBeforeCommand(f, cmd)
			if calls.Load() != wantCalls+1 {
				t.Fatalf("foreground did not dispatch after drain: calls=%d toast=%s", calls.Load(), f.ui.ToastText())
			}
			f.applyEvents()
			if kind == "submit" && f.ui.ComposerText() != "" {
				t.Fatal("submitted draft not cleared after admission")
			}
			if kind == "queue" && len(f.live.QueueSnapshot()) != 0 {
				t.Fatal("queued input not consumed after admission")
			}
		})
	}
}
