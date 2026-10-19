package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestTurnReservationAllowsBackgroundWorkButNotRestoration(t *testing.T) {
	t.Parallel()
	for _, queued := range []bool{false, true} {
		name := "ordinary"
		if queued {
			name = "queued"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			pm := code.NewProcessManager(ws)
			t.Cleanup(func() { pm.Close(context.WithoutCancel(t.Context())) })
			if _, err := pm.StartProcessContext(t.Context(), "sleep 60"); err != nil {
				t.Fatal(err)
			}
			live := engine.NewLiveRunner(&requestRecordingProvider{}, ws, "test", engine.WithProcessManager(pm))
			t.Cleanup(func() {
				if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
					t.Error(err)
				}
			})
			if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("strict transition with background work: %v", err)
			}
			var reservation *engine.RuntimeReservation
			if queued {
				_, id := live.QueueAppend("continue")
				if _, err := live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
					t.Fatalf("bypassed pending queue: %v", err)
				}
				reservation, err = live.ReserveQueuedTurn(id, "continue")
			} else {
				reservation, err = live.ReserveTurn()
			}
			if err != nil {
				t.Fatalf("dispatch with detached work: %v", err)
			}
			defer reservation.Release()
			if _, _, _, err := reservation.PersistenceSnapshot(); err != nil {
				t.Fatal(err)
			}
			if _, err := live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("double admission: %v", err)
			}
			if err := reservation.RestoreCheckpoint(rewind.Checkpoint{}, live.RunTarget()); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("dispatch reservation restored checkpoint: %v", err)
			}
			if err := reservation.RestoreConversation(nil, rewind.Target{}, code.Plan{}, live.RunTarget()); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("dispatch reservation restored conversation: %v", err)
			}
			if err := reservation.RunTurn(t.Context(), "continue", nil); err != nil {
				t.Fatal(err)
			}
			if live.QueueLen() != 0 {
				t.Fatal("queued dispatch did not consume its input")
			}
			if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("dispatch killed background work or weakened transitions: %v", err)
			}
		})
	}
}

func TestTurnReservationWaitsForCancelledProviderDrain(t *testing.T) {
	t.Parallel()
	provider := &watchedDrainProvider{taskID: make(chan string, 1), cancelled: make(chan struct{}), release: make(chan struct{})}
	live := reservationRunner(t, provider)
	done := make(chan error, 1)
	ctx := t.Context()
	go func() { done <- live.RunTurn(ctx, "hold") }()
	var release sync.Once
	defer func() { live.CancelTurn(); release.Do(func() { close(provider.release) }); <-done }()
	<-provider.taskID
	if !live.CancelTurn() {
		t.Fatal("cancel not accepted")
	}
	<-provider.cancelled
	if _, err := live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("admitted before cancellation cleanup: %v", err)
	}
	_, id := live.QueueAppend("queued")
	if _, err := live.ReserveQueuedTurn(id, "queued"); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("queued admission during cancellation cleanup: %v", err)
	}
	release.Do(func() { close(provider.release) })
}
