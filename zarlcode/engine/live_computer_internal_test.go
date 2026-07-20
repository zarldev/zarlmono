package engine

import (
	"context"
	"errors"
	"testing"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestLiveComputerSessionUsesApplicationContext(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	appCtx, cancelApp := context.WithCancel(t.Context())
	defer cancelApp()
	l := NewLiveRunner(nil, ws, nil, "local")
	l.SetContext(appCtx)

	fake := &fakeComputerSession{}
	factory := &fakeComputerFactory{session: fake}
	l.computer = &liveComputer{
		owner:      l,
		newSession: factory.newSession,
	}

	dispatchCtx, cancelDispatch := context.WithCancel(t.Context())
	if _, err := l.computer.Observe(dispatchCtx, model.ObserveRequest{}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	cancelDispatch()
	if err := factory.sessionCtx.Err(); err != nil {
		t.Fatalf("session context after dispatch cancellation = %v, want active", err)
	}

	cancelApp()
	if err := factory.sessionCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("session context after application cancellation = %v, want context canceled", err)
	}
}

func TestLiveComputerReusesSession(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	l := NewLiveRunner(nil, ws, nil, "local")
	fake := &fakeComputerSession{}
	l.computer = &liveComputer{owner: l, session: fake}

	obs, err := l.computer.Observe(t.Context(), model.ObserveRequest{IncludeText: true})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.Surface.Kind != model.SurfaceKinds.BROWSER {
		t.Fatalf("Observe kind = %v, want browser", obs.Surface.Kind)
	}
	if fake.observeCalls != 1 {
		t.Fatalf("observeCalls = %d, want 1", fake.observeCalls)
	}

	if _, err := l.computer.Act(t.Context(), model.ActionRequest{Action: model.Action{Kind: model.ActionKinds.SCROLL}}); err != nil {
		t.Fatalf("Act: %v", err)
	}
	if fake.actCalls != 1 {
		t.Fatalf("actCalls = %d, want 1", fake.actCalls)
	}

	if err := l.computer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("closeCalls = %d, want 1", fake.closeCalls)
	}
}

type fakeComputerFactory struct {
	sessionCtx context.Context
	session    computerSession
}

func (f *fakeComputerFactory) newSession(ctx context.Context, _ ...browser.Option) (computerSession, error) {
	f.sessionCtx = ctx
	return f.session, nil
}

type fakeComputerSession struct {
	*browser.Session
	observeCalls int
	actCalls     int
	closeCalls   int
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
	return nil
}
