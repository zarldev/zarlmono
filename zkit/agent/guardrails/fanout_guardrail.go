package guardrails

import (
	"context"
	"fmt"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// FanoutGuardrail caps how many DISTINCT calls a single task may
// make to a given tool. It addresses a different failure mode than
// [DecomposeGuardrail] (same-signature repeats) or [MemoSource]'s
// loop break (cache hits): the FAN OUT pattern where a model issues
// many different-argument calls to the same exploration tool —
// `read pkg/a.go`, `read pkg/b.go`, `read pkg/c.go`, … — instead of
// delegating to `spawn_agent`.
//
// The classic trigger is "read the pkg dir" against a workspace
// with 200 files. The model dutifully calls `read` once per file,
// consuming the context window with raw source the agent doesn't
// need to plan. spawn_agent's job is exactly this; the guardrail
// nudges the model back toward it.
//
// Per-tool budget (configured via WithLimit / NewFanoutGuardrail):
//
//	count < limit      → pass-through (model sees the real result)
//	count == limit     → call executed, but its result is replaced with a
//	                     Validation nudge naming spawn_agent
//	count > limit      → continued Validation rejection (with
//	                     escalating count) so the model can't
//	                     brute-force past the cap
//
// The counter is per-task and includes every dispatch, successful or
// failed. A failing repeat-loop (the same exploration tool rejected
// over and over) burns the budget too: it spends iterations and tokens
// just like a successful fan-out, and a model stuck on it needs the
// same nudge toward spawn_agent. (Same-signature and same-tool failure
// loops are also caught earlier and harder by [DecomposeGuardrail];
// this cap is the outer attempt ceiling that bounds the mixed
// success/failure case neither guardrail sees alone.)
type FanoutGuardrail struct {
	limits map[tools.ToolName]int

	mu      sync.Mutex
	buckets map[taskscope.ID]map[tools.ToolName]int
}

// NewFanoutGuardrail builds a guardrail with the given per-tool
// budgets. Tools not listed in limits are unbounded. The typical
// zarlcode wiring caps exploration tools (read, ls, grep) at a
// generous-but-finite count.
func NewFanoutGuardrail(limits map[tools.ToolName]int) *FanoutGuardrail {
	clone := make(map[tools.ToolName]int, len(limits))
	for k, v := range limits {
		if v > 0 {
			clone[k] = v
		}
	}
	return &FanoutGuardrail{
		limits:  clone,
		buckets: map[taskscope.ID]map[tools.ToolName]int{},
	}
}

// Name returns the guardrail's identifier.
func (g *FanoutGuardrail) Name() string { return "fanout" }

// Inspect runs after each tool dispatch. Every call — successful or
// failed — bumps the per-task counter for the tool and, once the budget
// is exhausted, the result is rewritten into a Validation rejection with
// a spawn_agent nudge.
func (g *FanoutGuardrail) Inspect(
	ctx context.Context,
	call tools.ToolCall,
	result *tools.ToolResult,
	execErr error,
) error {
	limit, ok := g.limits[call.ToolName]
	if !ok || limit <= 0 {
		return nil
	}
	count := g.bump(taskscope.IDFrom(ctx), call.ToolName)
	if count < limit {
		return nil
	}
	return tools.Validation("fanout", fmt.Sprintf(
		"%q has now been invoked %d times this task (cap %d). The fan-out pattern — "+
			"many small reads / lists driven directly from the orchestrator — burns context "+
			"the agent doesn't need for planning. Delegate further exploration to "+
			"`spawn_agent` with a specific question (e.g. \"map the public API of pkg/foo — "+
			"list each type and its purpose\") and act on the digest, not the raw bodies.",
		call.ToolName, count, limit))
}

// ForgetTask drops the per-task counter for id. Long-lived runners
// that serve many short tasks call this from a task-completion
// observer to keep memory bounded; short-lived runners can ignore.
func (g *FanoutGuardrail) ForgetTask(id taskscope.ID) {
	g.mu.Lock()
	delete(g.buckets, id)
	g.mu.Unlock()
}

func (g *FanoutGuardrail) bump(id taskscope.ID, name tools.ToolName) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.buckets[id] == nil {
		g.buckets[id] = map[tools.ToolName]int{}
	}
	g.buckets[id][name]++
	return g.buckets[id][name]
}
