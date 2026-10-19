package engine_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

func TestModeTargetReservationsAuthorizeOnlyExplicitTargetTransitions(t *testing.T) {
	t.Parallel()
	live := reservationRunner(t, &requestRecordingProvider{})
	before := live.RunTarget()
	update := engine.TargetUpdate{
		Provider: &requestRecordingProvider{},
		Spec:     engine.ProviderSpec{Name: "anthropic", Model: "next-model"},
		Window:   42,
	}

	ordinary, err := live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	if err := ordinary.ApplyTarget(update); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("ordinary turn target update: %v", err)
	}
	ordinary.Release()
	if got := live.RunTarget(); got.Spec != before.Spec || got.Provider != before.Provider {
		t.Fatalf("ordinary reservation published target: got=%+v before=%+v", got, before)
	}

	_, id := live.QueueAppend("queued prompt")
	if _, err := live.ReserveQueuedTargetTurn(id, "different prompt"); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("mismatched queue identity: %v", err)
	}
	reservation, err := live.ReserveQueuedTargetTurn(id, "queued prompt")
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	if err := reservation.ApplyTarget(update); err != nil {
		t.Fatal(err)
	}
	if got := live.RunTarget(); got.Spec != update.Spec || got.Provider != update.Provider || got.Window != update.Window {
		t.Fatalf("target reservation did not publish atomically: got=%+v want=%+v", got, update)
	}
	if live.QueueLen() != 1 {
		t.Fatal("target publication consumed queued input before turn admission")
	}
	if err := reservation.RunTurn(t.Context(), "queued prompt", nil); err != nil {
		t.Fatal(err)
	}
	if live.QueueLen() != 0 {
		t.Fatal("queued target turn did not consume its authorized queue head")
	}
}
