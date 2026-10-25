package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestModeTargetTurnPreservesDetachedWork(t *testing.T) {
	t.Parallel()
	for _, queued := range []bool{false, true} {
		name := "ordinary"
		if queued {
			name = "queued"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			workspace, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			processes := code.NewProcessManager(workspace)
			t.Cleanup(func() { processes.Close(context.WithoutCancel(t.Context())) })
			if _, err := processes.StartProcessContext(t.Context(), "sleep 60"); err != nil {
				t.Fatal(err)
			}
			live := engine.NewLiveRunner(&requestRecordingProvider{}, workspace, "before", engine.WithProcessManager(processes))
			t.Cleanup(func() { _ = live.Close(context.WithoutCancel(t.Context())) })
			var reservation *engine.RuntimeReservation
			if queued {
				_, id := live.QueueAppend("continue")
				reservation, err = live.ReserveQueuedTargetTurn(id, "continue")
			} else {
				reservation, err = live.ReserveTargetTurn()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer reservation.Release()
			if err := reservation.ApplyTarget(engine.TargetUpdate{Provider: &requestRecordingProvider{}, Spec: engine.ProviderSpec{Name: "test", Model: "after"}, Window: 42000}); err != nil {
				t.Fatal(err)
			}
			if err := reservation.RunTurn(t.Context(), "continue", nil); err != nil {
				t.Fatal(err)
			}
			if live.RunTarget().Model != "after" || live.QueueLen() != 0 {
				t.Fatal("target dispatch did not consume the selected input")
			}
			if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("target dispatch stopped detached work or weakened restore admission: %v", err)
			}
		})
	}
}
