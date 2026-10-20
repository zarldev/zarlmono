package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestLiveComputerCancelsStartup(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "caller"
		if shutdown {
			name = "shutdown"
		}
		t.Run(name, func(t *testing.T) {
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			live := engine.NewLiveRunner(nil, ws, "local", engine.WithComputerSessionFactory(func(ctx context.Context, _ ...browser.Option) (engine.ComputerSession, error) {
				close(started)
				select {
				case <-ctx.Done():
					return nil, context.Cause(ctx)
				case <-release:
					return nil, errors.New("fixture cleanup")
				}
			}))
			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("caller stopped startup")
			defer cancel(cause)
			done := make(chan error, 1)
			go func() { _, err := live.ComputerObserve(ctx, model.ObserveRequest{}); done <- err }()
			<-started
			if shutdown {
				closeCtx, stop := context.WithTimeout(t.Context(), time.Second)
				err = live.Close(closeCtx)
				stop()
			} else {
				cancel(cause)
			}
			select {
			case observed := <-done:
				if err != nil {
					t.Errorf("shutdown: %v", err)
				}
				want := cause
				if shutdown {
					want = engine.ErrRuntimeClosed
				}
				if !errors.Is(observed, want) {
					t.Errorf("observe = %v, want %v", observed, want)
				}
			case <-time.After(time.Second):
				close(release)
				<-done
				t.Error("startup did not observe cancellation")
			}
			if err := live.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLiveComputerCanceledStartupReportsCleanupError(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "caller"
		if shutdown {
			name = "shutdown"
		}
		t.Run(name, func(t *testing.T) {
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cleanupErr := errors.New("remove browser scratch directory")
			unpublished := &fakeComputerSession{closeErr: cleanupErr}
			replacement := &fakeComputerSession{}
			started := make(chan struct{})
			factoryCalls := 0
			live := engine.NewLiveRunner(nil, ws, "local", engine.WithComputerSessionFactory(func(ctx context.Context, _ ...browser.Option) (engine.ComputerSession, error) {
				factoryCalls++
				if factoryCalls == 1 {
					close(started)
					<-ctx.Done()
					return unpublished, nil
				}
				return replacement, nil
			}))
			testCtx := t.Context()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(testCtx), time.Second)
				defer cancel()
				if err := live.Close(ctx); err != nil {
					t.Errorf("cleanup runner: %v", err)
				}
			})
			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("caller stopped browser startup")
			done := make(chan struct{})
			var observed error
			go func() {
				defer close(done)
				_, observed = live.ComputerObserve(ctx, model.ObserveRequest{})
			}()
			defer func() { cancel(cause); <-done }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("browser factory did not start")
			}
			want := cause
			if shutdown {
				want = engine.ErrRuntimeClosed
				closeCtx, stop := context.WithTimeout(t.Context(), time.Second)
				err = live.Close(closeCtx)
				stop()
				if err != nil {
					t.Fatalf("shutdown: %v", err)
				}
			} else {
				cancel(cause)
			}
			<-done
			if !errors.Is(observed, want) || !errors.Is(observed, cleanupErr) {
				t.Errorf("observe = %v, want both %v and %v", observed, want, cleanupErr)
			}
			if !shutdown {
				if _, err := live.ComputerObserve(t.Context(), model.ObserveRequest{}); err != nil {
					t.Fatalf("retry after canceled startup: %v", err)
				}
				if factoryCalls != 2 || replacement.observeCalls != 1 {
					t.Errorf("retry: factory calls %d, replacement observations %d", factoryCalls, replacement.observeCalls)
				}
			}
			if err := live.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if unpublished.closeCalls != 1 || unpublished.observeCalls != 0 || unpublished.actCalls != 0 {
				t.Errorf("unpublished session: closes %d, observations %d, actions %d", unpublished.closeCalls, unpublished.observeCalls, unpublished.actCalls)
			}
		})
	}
}

func TestLiveComputerStartupCleanupDoesNotBlockCloseDeadline(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &blockingStartupCleanup{entered: make(chan struct{}), release: make(chan struct{})}
	started, finish := make(chan struct{}), make(chan struct{})
	live := engine.NewLiveRunner(nil, ws, "local", engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) {
		close(started)
		<-finish
		return s, nil
	}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := make(chan error, 1)
	go func() { _, err := live.ComputerObserve(ctx, model.ObserveRequest{}); observed <- err }()
	<-started
	cancel()
	close(finish)
	<-s.entered
	closeCtx, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()
	closed := make(chan error, 1)
	go func() { closed <- live.Close(closeCtx) }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("close = %v, want deadline", err)
		}
		close(s.release)
	case <-time.After(time.Second):
		t.Error("startup cleanup blocked Close before its deadline wait")
		close(s.release)
		<-closed
	}
	if err := <-observed; !errors.Is(err, context.Canceled) {
		t.Errorf("observe = %v", err)
	}
	if err := live.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.closeCalls != 1 {
		t.Fatalf("cleanup calls = %d", s.closeCalls)
	}
}

type blockingStartupCleanup struct {
	fakeComputerSession
	entered chan struct{}
	release chan struct{}
}

func (s *blockingStartupCleanup) Close() error {
	s.closeCalls++
	close(s.entered)
	<-s.release
	return nil
}
