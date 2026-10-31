package tui_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// lifecycleParent observes cancellation subscriptions through the context
// AfterFunc contract, without exposing the UI's private child context.
type lifecycleParent struct {
	context.Context
	attached chan struct{}
	detached chan struct{}
	detach   sync.Once
}

// Value keeps cancellation propagation on the parent's observable AfterFunc
// implementation rather than forwarding another context's private values.
func (*lifecycleParent) Value(any) any { return nil }

func (p *lifecycleParent) AfterFunc(f func()) func() bool {
	stop := context.AfterFunc(p.Context, f)
	close(p.attached)
	return func() bool {
		stopped := stop()
		p.detach.Do(func() { close(p.detached) })
		return stopped
	}
}

func TestStoppedForegroundSwarmReleasesApplicationContextSubscription(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "seed exact session")

	appCtx, cancelApp := context.WithCancel(t.Context())
	t.Cleanup(cancelApp)
	parent := &lifecycleParent{Context: appCtx, attached: make(chan struct{}), detached: make(chan struct{})}
	f.ui.SetApplicationContext(parent)
	_ = f.ui.StartSwarm("stop without cancelling the application")
	if !strings.Contains(f.ui.ToastText(), "swarm started") {
		t.Fatalf("swarm did not start: %s", f.ui.ToastText())
	}
	t.Cleanup(func() { _ = f.ui.StopSwarm() })

	ctx, stopTimeout := context.WithTimeout(t.Context(), 5*time.Second)
	defer stopTimeout()
	select {
	case <-parent.attached:
	case <-ctx.Done():
		t.Fatal("foreground swarm did not subscribe to its application context")
	}
	_ = f.ui.StopSwarm()
	select {
	case <-parent.detached:
	case <-ctx.Done():
		t.Fatal("stopped foreground swarm retained its application context subscription")
	}
	if err := appCtx.Err(); err != nil {
		t.Fatalf("stopping the swarm cancelled the application: %v", err)
	}
}
