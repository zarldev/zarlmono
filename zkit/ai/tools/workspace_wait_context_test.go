package tools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type waitContextObserver struct {
	started func(context.Context)
	ended   func(context.Context, tools.WorkspaceWaitOutcome)
}

func (o waitContextObserver) OnWorkspaceWaitStarted(ctx context.Context, _ tools.WorkspaceWaitStarted) {
	o.started(ctx)
}

func (o waitContextObserver) OnWorkspaceWaitEnded(ctx context.Context, e tools.WorkspaceWaitEnded) {
	o.ended(ctx, e.Outcome)
}

func TestWorkspaceWaitObserverReceivesOperationContext(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "acquired"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			coordinator := tools.NewWorkspaceCoordinator()
			holder, err := coordinator.Acquire("holder", tools.WorkspaceAccesses.WRITE)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Release()
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			cause := errors.New("stop waiting")
			started, ended := false, false
			observer := waitContextObserver{
				started: func(got context.Context) {
					started = true
					if got != ctx {
						t.Error("wait start replaced operation context")
					}
					if cancelled {
						cancel(cause)
					} else {
						holder.Release()
					}
				},
				ended: func(got context.Context, outcome tools.WorkspaceWaitOutcome) {
					ended = true
					want := tools.WorkspaceWaitOutcomes.WORKSPACEWAITACQUIRED
					if cancelled {
						want = tools.WorkspaceWaitOutcomes.WORKSPACEWAITCANCELLED
						if !errors.Is(context.Cause(got), cause) {
							t.Errorf("wait end lost cancellation cause: %v", context.Cause(got))
						}
					}
					if got != ctx || outcome != want {
						t.Errorf("wait end context/outcome changed: %v", outcome)
					}
				},
			}
			ctx = tools.ContextWithWorkspaceWaitObserver(ctx, observer)
			lease, err := coordinator.AcquirePathsWait(ctx, "waiter", tools.WorkspaceAccesses.READ, nil)
			if err == nil {
				lease.Release()
			}
			var want error
			if cancelled {
				want = context.Canceled
			}
			if !errors.Is(err, want) || !started || !ended {
				t.Fatalf("started=%v ended=%v error=%v; want %v", started, ended, err, want)
			}
		})
	}
}
