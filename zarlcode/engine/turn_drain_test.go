package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type delegatingDrainProvider struct {
	childStarted   chan struct{}
	childCancelled chan struct{}
	release        chan struct{}
	rootCalls      int
	panicRoot      bool
}

func (*delegatingDrainProvider) Name() string { return "drain" }

func (p *delegatingDrainProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if taskscope.DepthFrom(ctx) > 0 {
			close(p.childStarted)
			<-ctx.Done()
			close(p.childCancelled)
			<-p.release
			yield(llm.CompletionChunk{}, ctx.Err())
			return
		}
		p.rootCalls++
		if p.rootCalls == 1 {
			yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{ID: "spawn", Type: "function", Function: llm.ToolCallFunction{Name: "agent_spawn", Arguments: `{"prompt":"wait","mode":"implement"}`}}}}, nil)
			return
		}
		<-ctx.Done()
		if p.panicRoot {
			panic("root provider panic")
		}
		yield(llm.CompletionChunk{}, ctx.Err())
	}
}

func TestTurnDrainJoinsChildrenBeforeClosingDependencies(t *testing.T) {
	for _, tc := range []struct {
		name      string
		headless  bool
		panicRoot bool
	}{
		{name: "interactive"},
		{name: "headless", headless: true},
		{name: "interactive-panic", panicRoot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			synctest.Test(t, func(t *testing.T) {
				provider := &delegatingDrainProvider{childStarted: make(chan struct{}), childCancelled: make(chan struct{}), release: make(chan struct{})}
				provider.panicRoot = tc.panicRoot
				computer := &fakeComputerSession{}
				live := engine.NewLiveRunner(provider, ws, "local", engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) { return computer, nil }))
				live.SetLimits(0, 5, 5, 1)
				if _, err := live.ComputerObserve(t.Context(), model.ObserveRequest{}); err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				release := sync.OnceFunc(func() { close(provider.release) })
				defer func() {
					release()
					if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
						t.Error(err)
					}
					<-done
				}()
				ctx := t.Context()
				go func() {
					defer close(done)
					defer func() {
						if recovered := recover(); (recovered != nil) != tc.panicRoot {
							t.Errorf("turn panic = %v, expected panic = %v", recovered, tc.panicRoot)
						}
					}()
					if tc.headless {
						live.RunHeadless(ctx, "delegate", 5)
					} else {
						_ = live.RunTurn(ctx, "delegate")
					}
				}()
				<-provider.childStarted
				if result := live.RunHeadless(t.Context(), "overlapping turn", 1); result.Err == nil {
					t.Fatal("headless turn replaced the active turn owner")
				}
				waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				if err := live.Close(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Close = %v, want deadline", err)
				}
				synctest.Wait()
				select {
				case <-provider.childCancelled:
				default:
					t.Error("child was not cancelled")
				}
				select {
				case <-done:
					t.Error("turn drained before child exited")
				default:
				}
				if computer.closeCalls != 0 {
					t.Error("dependency closed before child exited")
				}
				release()
				if err := live.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				<-done
				if result := live.RunHeadless(t.Context(), "after close", 1); result.Err == nil {
					t.Fatal("headless turn started after shutdown")
				}
				if computer.closeCalls != 1 {
					t.Errorf("dependency closed %d times, want 1", computer.closeCalls)
				}
			})
		})
	}
}
