package engine_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type reservedBrowser struct {
	started, release chan struct{}
	closed           atomic.Bool
}

func (b *reservedBrowser) Observe(context.Context, model.ObserveRequest) (model.Observation, error) {
	close(b.started)
	<-b.release
	return model.Observation{}, nil
}
func (*reservedBrowser) Act(context.Context, model.ActionRequest) (model.Observation, error) {
	return model.Observation{}, nil
}
func (b *reservedBrowser) Close() error { b.closed.Store(true); return nil }

func TestShutdownWaitsForAdmittedBrowserOperation(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		browserSession := &reservedBrowser{started: make(chan struct{}), release: make(chan struct{})}
		live := engine.NewLiveRunner(&requestRecordingProvider{}, ws, "saved-model", engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) { return browserSession, nil }))
		done := make(chan error, 1)
		release := sync.OnceFunc(func() { close(browserSession.release) })
		defer func() {
			release()
			<-done
			if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
				t.Error(err)
			}
		}()
		operationCtx := t.Context()
		go func() { _, err := live.ComputerObserve(operationCtx, model.ObserveRequest{}); done <- err }()
		<-browserSession.started
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := live.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close: %v", err)
		}
		if browserSession.closed.Load() {
			t.Fatal("closed browser during admitted operation")
		}
		assertRuntimeClosed(t, live)
		release()
	})
}

func assertRuntimeClosed(t *testing.T, live *engine.LiveRunner) {
	t.Helper()
	if _, err := live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeClosed) {
		t.Fatalf("reserve: %v", err)
	}
	if err := live.RunTurn(t.Context(), "blocked"); !errors.Is(err, engine.ErrRuntimeClosed) {
		t.Fatalf("turn: %v", err)
	}
	if result := live.RunHeadless(t.Context(), "blocked", 1); !errors.Is(result.Err, engine.ErrRuntimeClosed) {
		t.Fatalf("headless: %v", result.Err)
	}
	if _, err := live.CompactNow(t.Context()); !errors.Is(err, engine.ErrRuntimeClosed) {
		t.Fatalf("compact: %v", err)
	}
	if _, err := live.ComputerAct(t.Context(), model.ActionRequest{}); !errors.Is(err, engine.ErrRuntimeClosed) {
		t.Fatalf("act: %v", err)
	}
	if _, err := live.ComputerObserve(t.Context(), model.ObserveRequest{}); !errors.Is(err, engine.ErrRuntimeClosed) {
		t.Fatalf("observe: %v", err)
	}
	if _, _, err := live.KillProcess("none", "TERM"); !errors.Is(err, engine.ErrRuntimeClosed) {
		t.Fatalf("process: %v", err)
	}
	if ins := live.Inspect(t.Context()); len(ins.Errors) == 0 || ins.WorkspaceRoot != "" {
		t.Fatal("inspection assembled during closing")
	}
}

func TestInspectionUnavailableDuringReservation(t *testing.T) {
	t.Parallel()
	live := reservationRunner(t, &requestRecordingProvider{})
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	if ins := live.Inspect(t.Context()); len(ins.Errors) == 0 || ins.WorkspaceRoot != "" {
		t.Fatal("inspection assembled during reservation")
	}
}

type serializedTurnProvider struct{ requests atomic.Int32 }

func (*serializedTurnProvider) Name() string { return "serialized" }
func (p *serializedTurnProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.requests.Add(1)
		yield(llm.CompletionChunk{Content: "answer"}, nil)
	}
}

func TestConcurrentInteractiveTurnsSerialize(t *testing.T) {
	t.Parallel()
	provider := &serializedTurnProvider{}
	live := reservationRunner(t, provider)
	var group sync.WaitGroup
	for range 24 {
		group.Go(func() {
			if err := live.RunTurn(t.Context(), "prompt"); err != nil {
				t.Errorf("serialized turn: %v", err)
			}
		})
	}
	group.Wait()
	if got := provider.requests.Load(); got != 24 {
		t.Fatalf("requests = %d", got)
	}
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
}
