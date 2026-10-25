package engine_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

// countingSwarmProvider records model calls and answers with a single completed
// text chunk, exercising the observation-capable openai wire projection.
type countingSwarmProvider struct {
	calls atomic.Int32
	reply string
}

func (p *countingSwarmProvider) Complete(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	p.calls.Add(1)
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: p.reply, FinishReason: llm.FinishReasons.STOP}, nil)
	}
}

func (*countingSwarmProvider) Name() string { return "openai" }

func recordedSwarmRuntime(t *testing.T, root string, provider llm.Provider) *engine.LiveRunner {
	t.Helper()
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	wire := openai.NewProvider("fixture-key", openai.WithHTTPClient(&http.Client{Transport: swarmTransport{provider: provider}}))
	live := engine.NewLiveRunner(wire, workspace, "fixture-model", engine.WithReadOnlyTasks())
	live.SetProviderSpec(wire, engine.ProviderSpec{Name: "openai", Model: "fixture-model"})
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
		if err := workspace.Close(); err != nil {
			t.Error(err)
		}
	})
	return live
}
func recordedSwarmStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	store, err := db.Open(t.Context(), t.TempDir()+"/recorded.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const sessionID = "recorded-session"
	if err := store.SaveActiveSession(t.Context(), db.SessionRecord{ID: sessionID, Workspace: "/workspace"}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(t.Context(), sessionID, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), db.SessionCheckpoint{SessionID: sessionID, ID: "initial", SourceSessionID: sessionID, Workspace: "/workspace", BoundaryID: "prompt", FormatVersion: 2, Payload: []byte(`{}`), History: ref}); err != nil {
		t.Fatal(err)
	}
	return store, sessionID
}

func TestAddRecordedRunsOncePreservesIdentityAndRecords(t *testing.T) {
	store, sessionID := recordedSwarmStore(t)
	provider := &countingSwarmProvider{reply: "done"}
	live := recordedSwarmRuntime(t, t.TempDir(), provider)
	goal := engine.NewSwarm("recorded member")

	var handle *engine.RecordedSwarmActivation
	if _, err := goal.AddRecorded("member", live, "Inspect and report.", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		handle = a
		if got := provider.calls.Load(); got != 0 {
			t.Errorf("provider dispatched before RunRecorded: %d calls", got)
		}
		wantID := a.Spec.ID
		a.Spec.ID = "forged"
		a.Spec.Prompt = "forged assignment"
		res := a.RunRecorded(ctx, store, sessionID)
		if res.ID != wantID {
			t.Errorf("result ID = %q, want %q", res.ID, wantID)
		}
		if res.Err != nil || res.Reason != runner.TerminalCompleted {
			t.Errorf("run recorded = %+v", res)
		}
		duplicate := a.RunRecorded(ctx, store, sessionID)
		if !errors.Is(duplicate.Err, engine.ErrRuntimeBusy) {
			t.Errorf("duplicate RunRecorded error = %v, want ErrRuntimeBusy", duplicate.Err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}

	snap, _ := goal.Snapshot("member")
	if snap.State != swarm.States.IDLE || snap.Activations != 1 || snap.LastError != nil {
		t.Fatalf("member after run: %+v", snap)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls.Load())
	}
	if batches := live.RecordedHistory(sessionID); len(batches) == 0 {
		t.Fatal("no recorded history batches captured")
	}
	before := provider.calls.Load()
	late := handle.RunRecorded(t.Context(), store, sessionID)
	if !errors.Is(late.Err, engine.ErrRuntimeBusy) {
		t.Errorf("late RunRecorded error = %v, want ErrRuntimeBusy", late.Err)
	}
	if provider.calls.Load() != before {
		t.Error("late RunRecorded dispatched a model call")
	}
}

func TestAddRecordedNeverRunSuspends(t *testing.T) {
	provider := &countingSwarmProvider{reply: "done"}
	live := recordedSwarmRuntime(t, t.TempDir(), provider)
	goal := engine.NewSwarm("never run")

	if _, err := goal.AddRecorded("member", live, "Inspect.", func(context.Context, *engine.RecordedSwarmActivation) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	snap, _ := goal.Snapshot("member")
	if snap.State != swarm.States.SUSPENDED || !errors.Is(snap.LastError, engine.ErrSwarmActivationNotRun) {
		t.Fatalf("never-run member: %+v", snap)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("never-run dispatched %d model calls", provider.calls.Load())
	}
}

func TestAddRecordedHostErrorSuspendsAndPreservesResult(t *testing.T) {
	store, sessionID := recordedSwarmStore(t)
	provider := &countingSwarmProvider{reply: "done"}
	live := recordedSwarmRuntime(t, t.TempDir(), provider)
	goal := engine.NewSwarm("host error")

	hostErr := errors.New("save acknowledgement failed")
	if _, err := goal.AddRecorded("member", live, "Inspect.", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		res := a.RunRecorded(ctx, store, sessionID)
		if res.Err != nil {
			t.Errorf("run recorded: %v", res.Err)
		}
		return hostErr
	}); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	snap, _ := goal.Snapshot("member")
	if snap.State != swarm.States.SUSPENDED || !errors.Is(snap.LastError, hostErr) {
		t.Fatalf("host-error member: %+v", snap)
	}
	if len(snap.Messages) == 0 {
		t.Fatal("completed engine result messages were not preserved through host error")
	}
}

func TestAddRecordedCancelledContextUnsuccessful(t *testing.T) {
	store, sessionID := recordedSwarmStore(t)
	provider := &countingSwarmProvider{reply: "done"}
	live := recordedSwarmRuntime(t, t.TempDir(), provider)
	goal := engine.NewSwarm("cancelled")

	if _, err := goal.AddRecorded("member", live, "Inspect.", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		res := a.RunRecorded(cancelled, store, sessionID)
		if !errors.Is(res.Err, context.Canceled) {
			t.Errorf("cancelled result error = %v, want context.Canceled", res.Err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	snap, _ := goal.Snapshot("member")
	if snap.State != swarm.States.SUSPENDED || !errors.Is(snap.LastError, context.Canceled) {
		t.Fatalf("cancelled member: %+v", snap)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("cancelled dispatch made %d model calls", provider.calls.Load())
	}
}

func TestAddRecordedHoldsReactivationUntilSettlement(t *testing.T) {
	store, sessionID := recordedSwarmStore(t)
	provider := &countingSwarmProvider{reply: "done"}
	live := recordedSwarmRuntime(t, t.TempDir(), provider)
	goal := engine.NewSwarm("settlement gate")

	dispatched := make(chan struct{})
	var dispatchedOnce sync.Once
	ack := make(chan struct{})
	if _, err := goal.AddRecorded("member", live, "Inspect and report.", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		res := a.RunRecorded(ctx, store, sessionID)
		if res.Err != nil {
			t.Errorf("run recorded: %v", res.Err)
		}
		dispatchedOnce.Do(func() { close(dispatched) })
		select {
		case <-ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)

	<-dispatched
	snap, _ := goal.Snapshot("member")
	if snap.State != swarm.States.RUNNING || snap.Activations != 1 {
		t.Fatalf("before save ack: %+v", snap)
	}

	draft, _ := board.ParseDraft("summary", "host evidence", "", 0)
	if _, err := goal.Post(t.Context(), draft); err != nil {
		t.Fatal(err)
	}
	snap, _ = goal.Snapshot("member")
	if snap.Activations != 1 {
		t.Fatalf("reactivated before save ack: %+v", snap)
	}

	close(ack)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	snap, _ = goal.Snapshot("member")
	if snap.Activations != 2 || snap.State != swarm.States.IDLE {
		t.Fatalf("after save ack: %+v", snap)
	}
}
