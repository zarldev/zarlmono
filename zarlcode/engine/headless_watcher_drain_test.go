package engine_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type watchedDrainProvider struct {
	cancelled, release chan struct{}
	taskID             chan string
}

func (*watchedDrainProvider) Name() string { return "watched-drain" }
func (p *watchedDrainProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		taskID, _ := req.Options["prompt_cache_key"].(string)
		p.taskID <- taskID
		<-ctx.Done()
		close(p.cancelled)
		<-p.release
		yield(llm.CompletionChunk{}, ctx.Err())
	}
}

type watchedPanicProvider struct{}

func (*watchedPanicProvider) Name() string { return "watched-panic" }
func (*watchedPanicProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(func(llm.CompletionChunk, error) bool) {
		panic("watched provider panic")
	}
}

func TestHeadlessWatcherJoinsRootBeforeClosingDependencies(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	synctest.Test(t, func(t *testing.T) {
		provider := &watchedDrainProvider{
			cancelled: make(chan struct{}),
			release:   make(chan struct{}),
			taskID:    make(chan string, 1),
		}
		computer := &fakeComputerSession{}
		live := engine.NewLiveRunner(provider, ws, "local", engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) { return computer, nil }))
		live.SetEarlyStopCommand([]string{"true"})
		if _, err := live.ComputerObserve(t.Context(), model.ObserveRequest{}); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		results := make(chan runner.TaskResult, 1)
		release := sync.OnceFunc(func() { close(provider.release) })
		defer func() { release(); _ = live.Close(context.WithoutCancel(t.Context())); <-done }()
		ctx := t.Context()
		go func() {
			defer close(done)
			results <- live.RunHeadless(ctx, "wait", 1)
		}()
		originalTaskID := <-provider.taskID
		<-provider.cancelled
		// Let the pursuit watcher's bounded drain expire in virtual time.
		time.Sleep(time.Minute)
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("headless returned with a live root attempt")
		default:
		}
		if result := live.RunHeadless(t.Context(), "overlap", 1); !errors.Is(result.Err, engine.ErrRuntimeBusy) {
			t.Fatalf("overlap: %v", result.Err)
		}
		waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := live.Close(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close: %v", err)
		}
		if computer.closeCalls != 0 {
			t.Fatal("dependency closed before root settled")
		}
		release()
		if err := live.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-done
		if computer.closeCalls != 1 {
			t.Fatalf("close calls: %d", computer.closeCalls)
		}
		result := <-results
		if result.Reason != runner.TerminalError {
			t.Errorf("reason = %v, want TerminalError", result.Reason)
		}
		if !errors.Is(result.Err, pursue.ErrAttemptCancelDrainTimeout) {
			t.Errorf("error = %v, want ErrAttemptCancelDrainTimeout", result.Err)
		}
		if string(result.ID) != originalTaskID {
			t.Errorf("result task ID = %q, want original %q", result.ID, originalTaskID)
		}
		if len(result.Messages) == 0 {
			t.Fatal("eventual attempt messages were discarded")
		}
		if snapshot := live.ContextSnapshot(); !reflect.DeepEqual(snapshot, result.Messages) {
			t.Errorf("committed context differs from result messages: got %#v want %#v", snapshot, result.Messages)
		}
	})
}

func TestHeadlessWatcherRecoversAttemptPanic(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })

	synctest.Test(t, func(t *testing.T) {
		live := engine.NewLiveRunner(&watchedPanicProvider{}, ws, "local")
		live.SetEarlyStopCommand([]string{"true"})
		t.Cleanup(func() { _ = live.Close(context.WithoutCancel(t.Context())) })

		result := live.RunHeadless(t.Context(), "panic", 1)
		if result.Reason != runner.TerminalError {
			t.Errorf("reason = %v, want TerminalError", result.Reason)
		}
		if result.Err == nil || !strings.Contains(result.Err.Error(), "watched provider panic") {
			t.Errorf("error = %v, want recovered provider panic", result.Err)
		}
		if result.ID == "" {
			t.Error("result task ID is empty")
		}
	})
}
