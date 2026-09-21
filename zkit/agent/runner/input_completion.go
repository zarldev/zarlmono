package runner

import (
	"context"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// completeNoToolTurn settles a provisional response or returns false to resume
// provider work. Waiting happens before corrections; sealing happens only after
// they accept completion. The Run goroutine remains the sole history owner.
func (t *taskRun) completeNoToolTurn(ctx context.Context, clean string, usage *llm.Usage, preparation time.Duration) (TaskResult, bool) {
	r := t.r
	published := false
	publish := func() {
		if !published {
			r.publishIterationCompleted(ctx, t.spec, t.iter, usage, t.lastUsage, t.messages, t.toolSurface, preparation, 0)
			published = true
		}
	}
	for {
		if err := context.Cause(ctx); err != nil {
			return t.cancelled(ctx, err), true
		}
		if r.inputs != nil && r.inputs.Ready(t.spec.ID).Outstanding {
			publish()
			if t.iter+1 == t.maxIter {
				return t.maxedOut(ctx), true
			}
			resume, err := t.waitForInputs(ctx)
			if err != nil {
				return t.cancelled(ctx, err), true
			}
			if resume {
				return TaskResult{}, false
			}
			continue
		}
		if t.applyTurnQuality(clean, false) || t.holdCompletion(clean) {
			return TaskResult{}, false
		}
		if r.inputs == nil || r.inputs.Finish(t.spec.ID) {
			break
		}
		// Start won the race with atomic finish. Recheck without another request.
	}
	publish()
	if observer, ok := r.compactor.(compact.UsageObserver); ok {
		observer.ObserveUsage(t.lastUsage)
	}
	return t.completed(ctx), true
}
