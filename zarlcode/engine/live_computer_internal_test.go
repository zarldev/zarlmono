package engine

import (
	"context"
	"testing"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

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
