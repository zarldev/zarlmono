package engine_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestLiveComputerSessionUsesDetachedContext(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	appCtx, cancelApp := context.WithCancel(t.Context())
	fake := &fakeComputerSession{}
	sessionCtxCh := make(chan context.Context, 1)
	live := engine.NewLiveRunner(nil, ws, "local", engine.WithComputerSessionFactory(func(ctx context.Context, _ ...browser.Option) (engine.ComputerSession, error) {
		sessionCtxCh <- ctx
		return fake, nil
	}))

	if _, err := live.ComputerObserve(appCtx, model.ObserveRequest{}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	sessionCtx := <-sessionCtxCh
	cancelApp()
	if err := sessionCtx.Err(); err != nil {
		t.Fatalf("owned session context after caller cancellation = %v, want active until Close", err)
	}
	if err := live.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestLiveComputerReusesSession(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	fake := &fakeComputerSession{}
	factoryCalls := 0
	live := engine.NewLiveRunner(nil, ws, "local", engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) {
		factoryCalls++
		return fake, nil
	}))

	obs, err := live.ComputerObserve(t.Context(), model.ObserveRequest{IncludeText: true})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.Surface.Kind != model.SurfaceKinds.BROWSER {
		t.Fatalf("Observe kind = %v, want browser", obs.Surface.Kind)
	}
	if _, err := live.ComputerAct(t.Context(), model.ActionRequest{Action: model.Action{Kind: model.ActionKinds.SCROLL}}); err != nil {
		t.Fatalf("Act: %v", err)
	}
	if factoryCalls != 1 || fake.observeCalls != 1 || fake.actCalls != 1 {
		t.Fatalf("calls = factory %d, observe %d, act %d; want 1 each", factoryCalls, fake.observeCalls, fake.actCalls)
	}
	if err := live.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", fake.closeCalls)
	}
}

type fakeComputerSession struct {
	observeCalls int
	actCalls     int
	closeCalls   int
	closeErr     error
}

func (f *fakeComputerSession) Observe(context.Context, model.ObserveRequest) (model.Observation, error) {
	f.observeCalls++
	return model.Observation{Surface: model.SurfaceInfo{Kind: model.SurfaceKinds.BROWSER}}, nil
}

func (f *fakeComputerSession) Act(context.Context, model.ActionRequest) (model.Observation, error) {
	f.actCalls++
	return model.Observation{Surface: model.SurfaceInfo{Kind: model.SurfaceKinds.BROWSER}}, nil
}

func (f *fakeComputerSession) Close() error {
	f.closeCalls++
	return f.closeErr
}
