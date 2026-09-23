package engine_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestOrdinaryPersistenceSnapshotDoesNotQualifyExactReplay(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "custom-model", BaseURL: "https://model.example.test/v1"})
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "owned context"}})
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	messages, target, exact, err := reservation.PersistenceSnapshot()
	if err != nil || exact || target.Model != "custom-model" || len(messages) != 1 {
		t.Fatalf("ordinary snapshot = %v, %v, %v, %v", messages, target, exact, err)
	}
	messages[0].Content = "modified copy"
	if live.ContextSnapshot()[0].Content != "owned context" {
		t.Fatal("persistence snapshot aliases live context")
	}
	if _, _, err := reservation.Snapshot(); !errors.Is(err, rewind.ErrTarget) {
		t.Fatalf("custom route qualified for exact replay: %v", err)
	}
	reservation.Release()
	if _, _, _, err := reservation.PersistenceSnapshot(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("released reservation returned context: %v", err)
	}
}
