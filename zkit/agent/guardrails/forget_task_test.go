package guardrails_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// forgettingSource is a fakeSource that records ForgetTask forwarding,
// standing in for a wrapped layer with per-task state (a pipeline wrapper,
// another MemoSource, ...).
type forgettingSource struct {
	fakeSource
	forgotten []taskscope.ID
}

func (f *forgettingSource) ForgetTask(id taskscope.ID) {
	f.forgotten = append(f.forgotten, id)
}

// forgettingGuard records ForgetTask forwarding to a guardrail.
type forgettingGuard struct {
	forgotten []taskscope.ID
}

func (g *forgettingGuard) Name() string { return "forgetting" }
func (g *forgettingGuard) ForgetTask(id taskscope.ID) {
	g.forgotten = append(g.forgotten, id)
}

// ForgetTask must reach every layer that keeps per-task state: the guards
// AND the wrapped source. MemoSource forwards inward the same way, so a
// wrapper chain cleans up end to end no matter the nesting order.
func TestGuardedSource_ForgetTaskForwardsToGuardsAndInner(t *testing.T) {
	inner := &forgettingSource{}
	guard := &forgettingGuard{}
	gs := guardrails.NewGuardedSource(inner, guard)

	gs.ForgetTask(taskscope.ID("t1"))

	if len(guard.forgotten) != 1 || guard.forgotten[0] != taskscope.ID("t1") {
		t.Fatalf("guard saw %v, want [t1]", guard.forgotten)
	}
	if len(inner.forgotten) != 1 || inner.forgotten[0] != taskscope.ID("t1") {
		t.Fatalf("inner source saw %v, want [t1]", inner.forgotten)
	}
}
