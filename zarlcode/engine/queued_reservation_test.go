package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
)

func TestQueuedTurnReservationPreservesInputUntilConversion(t *testing.T) {
	live := reservationRunner(t, &requestRecordingProvider{})
	_, id := live.QueueAppend("queued prompt")
	live.QueueAppend("later input")
	if _, err := live.ReserveQueuedTurn(id, "stale text"); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatal(err)
	}
	r, err := live.ReserveQueuedTurn(id, "queued prompt")
	if err != nil {
		t.Fatal(err)
	}
	if live.QueueLen() != 2 {
		t.Fatal("reservation consumed input")
	}
	if _, appended := live.QueueAppend("blocked"); appended != 0 {
		t.Fatal("queue write admitted")
	}
	if err := r.RestoreCheckpoint(rewind.Checkpoint{}, live.RunTarget()); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.RunTurn(ctx, "queued prompt", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r.Release()
	if live.QueueLen() != 2 {
		t.Fatal("cancellation consumed input")
	}
	r, err = live.ReserveQueuedTurn(id, "queued prompt")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	if err := r.RunTurn(t.Context(), "queued prompt", nil); err != nil {
		t.Fatal(err)
	}
	for _, entry := range live.QueueSnapshot() {
		if entry.ID == id {
			t.Fatal("promoted head remains queued")
		}
	}
}
