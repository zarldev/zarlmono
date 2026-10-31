package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestRecordedSwarmCloseCancelsSeparatelyDispatchedWork(t *testing.T) {
	store, sessionID := recordedSwarmStore(t)
	started := make(chan struct{})
	provider := swarmProvider{complete: func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			close(started)
			<-ctx.Done()
			yield(llm.CompletionChunk{}, ctx.Err())
		}
	}}
	live := recordedSwarmRuntime(t, t.TempDir(), provider)
	goal := engine.NewSwarm("cancel dispatched activation")
	// A UI command has its own context, independent of the activation callback.
	dispatchCtx, cancelDispatch := context.WithCancel(t.Context())
	defer cancelDispatch()
	result := make(chan runner.TaskResult, 1)
	if _, err := goal.AddRecorded("member", live, "Inspect.", func(_ context.Context, activation *engine.RecordedSwarmActivation) error {
		result <- activation.RunRecorded(dispatchCtx, store, sessionID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("recorded provider did not start")
	}
	closeCtx, cancelClose := context.WithTimeout(t.Context(), time.Second)
	defer cancelClose()
	if err := goal.Close(closeCtx); err != nil {
		t.Fatalf("close did not cancel and drain recorded work: %v", err)
	}
	if got := <-result; got.Reason != runner.TerminalCancelled || !errors.Is(got.Err, swarm.ErrClosed) {
		t.Fatalf("recorded result reason=%v error=%v, want swarm cancellation", got.Reason, got.Err)
	}
	if dispatchCtx.Err() != nil {
		t.Fatal("closing a swarm canceled its caller's context")
	}
}
