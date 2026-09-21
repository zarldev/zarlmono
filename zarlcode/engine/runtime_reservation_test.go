package engine_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"syscall"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func reservationRunner(t *testing.T, provider llm.Provider) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(provider, ws, "saved-model")
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "saved-model", APIKey: "private-key-canary"})
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return live
}

func TestRuntimeReservationExcludesMutations(t *testing.T) {
	t.Parallel()
	live := reservationRunner(t, &requestRecordingProvider{})
	history := []llm.Message{{Role: llm.RoleUser, Content: "history"}}
	live.RestoreContext(history)
	target := live.RunTarget()
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("second reserve: %v", err)
	}
	if err := live.RunTurn(t.Context(), "blocked"); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("turn: %v", err)
	}
	if result := live.RunHeadless(t.Context(), "blocked", 1); !errors.Is(result.Err, engine.ErrRuntimeBusy) {
		t.Fatalf("headless: %v", result.Err)
	}
	if _, err := live.CompactNow(t.Context()); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("compact: %v", err)
	}
	if err := live.Close(t.Context()); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("close: %v", err)
	}
	if _, err := live.ComputerAct(t.Context(), model.ActionRequest{}); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("computer: %v", err)
	}
	if _, err := live.ComputerObserve(t.Context(), model.ObserveRequest{}); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("observe: %v", err)
	}
	if _, _, err := live.KillProcess("none", "TERM"); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("process: %v", err)
	}
	live.ClearContext()
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "future"}})
	live.SetModel("future")
	live.SetPlanMode(true)
	live.SetContextWindow(42)
	live.SetLimits(42, 42, 42, 42)
	live.ApplyTarget(engine.TargetUpdate{Provider: &requestRecordingProvider{}, Spec: engine.ProviderSpec{Name: "future", Model: "future"}, Window: 42})
	if _, id := live.QueueAppend("blocked"); id != 0 {
		t.Fatal("queue append admitted")
	}
	if _, id := live.QueueAppendControl("blocked"); id != 0 {
		t.Fatal("control append admitted")
	}
	if depth := live.QueueInjector().Append("blocked"); depth != 0 {
		t.Fatal("MCP input admitted")
	}
	if live.QueueInput("blocked") != 0 || live.QueueLen() != 0 || live.QueueClear() != 0 || live.QueueRemove(1) || live.QueueUpdate(1, "blocked") {
		t.Fatal("queue mutated")
	}
	if !reflect.DeepEqual(live.RunTarget(), target) || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("reserved state mutated")
	}
	snapshot, savedTarget, err := reservation.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot[0].Content = "caller mutation"
	if savedTarget.Provider != "openai" || savedTarget.Model != "saved-model" || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("snapshot identity or ownership")
	}
	reservation.Release()
	reservation.Release()
	if _, _, err := reservation.Snapshot(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("released snapshot: %v", err)
	}
	if _, id := live.QueueAppend("after release"); id == 0 {
		t.Fatal("queue remained blocked")
	}
	if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("queued input: %v", err)
	}
	live.QueueClear()
	next, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
}

func TestRuntimeReservationRejectsActiveTurn(t *testing.T) {
	t.Parallel()
	provider := &blockingProvider{started: make(chan struct{})}
	live := reservationRunner(t, provider)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { done <- live.RunTurn(ctx, "hold") }()
	defer func() { cancel(); <-done }()
	<-provider.started
	if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("active turn: %v", err)
	}
}

func TestRuntimeReservationRejectsBackgroundProcesses(t *testing.T) {
	t.Parallel()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pm := code.NewProcessManager(ws)
	defer pm.Close(context.WithoutCancel(t.Context()))
	process, err := pm.StartProcessContext(t.Context(), "sleep 60")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pm.Kill(process, syscall.SIGKILL) }()
	live := engine.NewLiveRunner(&requestRecordingProvider{}, ws, "saved-model", engine.WithProcessManager(pm))
	defer func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	}()
	if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("background process: %v", err)
	}
}

func TestRuntimeReservationToTurnHasNoGap(t *testing.T) {
	t.Parallel()
	provider := &blockingProvider{started: make(chan struct{})}
	live := reservationRunner(t, provider)
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- reservation.RunTurn(ctx, "hold", nil) }()
	defer func() { cancel(); <-done; reservation.Release() }()
	<-provider.started
	if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("converted turn: %v", err)
	}
	if _, id := live.QueueAppend("steer during turn"); id == 0 {
		t.Fatal("reservation was not converted to shared turn admission")
	}
}

func TestRuntimeReservationConcurrentRelease(t *testing.T) {
	t.Parallel()
	live := reservationRunner(t, &requestRecordingProvider{})
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() { reservation.Release() })
	}
	group.Wait()
	next, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
}

func TestRuntimeReservationRestoreExactContext(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	workspace := liveWorkspace(t, live)
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	_, target, err := reservation.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	builder := transcript.NewBuilder()
	builder.AddUser("historical prompt")
	builder.AppendAssistant("settled", "", "historical answer")
	builder.FinishTurn("settled")
	canonical, err := builder.Thread().CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	history := []llm.Message{{Role: llm.RoleUser, Content: "historical prompt"}, {Role: llm.RoleAssistant, Content: "historical answer"}}
	checkpoint, err := rewind.Capture(rewind.CaptureInput{ID: "checkpoint", SessionID: "source", Workspace: workspace, Boundary: rewind.Boundary{PromptID: "next", PromptText: "edit me", SettledTurnID: "settled", EventWatermark: canonical.Revision()}, Transcript: canonical, Context: history, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.RestoreCheckpoint(checkpoint, live.RunTarget()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("restored context changed")
	}
	if len(provider.requests) != 0 {
		t.Fatal("restore submitted prompt")
	}
}

func liveWorkspace(t *testing.T, live *engine.LiveRunner) string {
	t.Helper()
	return live.Inspect(t.Context()).WorkspaceRoot
}
