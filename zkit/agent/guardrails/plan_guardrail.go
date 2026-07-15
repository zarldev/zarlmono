package guardrails

import (
	"context"
	"fmt"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// PlanGuardrail enforces a plan-first workflow within a task: the first
// workspace-changing call (a file edit, a bash command — anything whose spec
// reports ChangesWorkspace) is refused until the model has successfully
// called the planning tool at least once. Read / search / explore tools and
// the planning tool itself are always allowed, so the model can investigate
// freely and produce a plan; only durable changes are gated. Spawned
// sub-agents (task depth > 0) are exempt: the parent already chose delegation,
// and the child may be running under a narrow iteration budget where a separate
// plan-first loop is counterproductive.
//
// Motivation: weak / local models skip planning and dive straight into edits,
// then thrash. An advisory "please plan first" in the system prompt is easy
// to ignore; a Before() refusal is not — the changing tool never runs until a
// plan exists. This is the enforcement complement to the prompt's planning
// contract, mirroring how DecomposeGuardrail.Before hard-stops a doomed retry
// loop that advisory text alone can't.
//
// Mutation is read from the tool's own ChangesWorkspace capability via the
// snapshot Iterable, so there is no hardcoded tool-name list to drift and a
// runtime-registered mutating tool is gated the moment it appears. An
// unresolvable name reads as non-changing — the safe default, since the gate
// must never block a call it cannot classify.
//
// State is per-task: planned[id] flips true when the plan tool succeeds and
// is dropped on ForgetTask. Safe for concurrent Before / Inspect / ForgetTask
// — GuardedSource dispatches a batch in parallel.
type PlanGuardrail struct {
	iter     tools.Iterable
	planTool tools.ToolName

	mu      sync.Mutex
	planned map[taskscope.ID]bool
}

// NewPlanGuardrail wires a plan-first gate that resolves tool capabilities
// through iter and treats a successful planTool call as satisfying the gate.
func NewPlanGuardrail(iter tools.Iterable, planTool tools.ToolName) *PlanGuardrail {
	return &PlanGuardrail{
		iter:     iter,
		planTool: planTool,
		planned:  map[taskscope.ID]bool{},
	}
}

// Name returns the guardrail's identifier.
func (g *PlanGuardrail) Name() string { return "plan_first" }

// Before refuses a workspace-changing call until this task has a plan. The
// planning tool, spawned sub-agent tasks, and every non-changing tool pass
// through unconditionally, so the model can read, search, and plan without
// obstruction.
func (g *PlanGuardrail) Before(ctx context.Context, call tools.ToolCall) error {
	if call.ToolName == g.planTool {
		return nil
	}
	if taskscope.DepthFrom(ctx) > 0 {
		return nil
	}
	if !g.changesWorkspace(ctx, call.ToolName) {
		return nil
	}
	if g.isPlanned(taskscope.IDFrom(ctx)) {
		return nil
	}
	return tools.Validation("plan_first", fmt.Sprintf(
		"%q changes the workspace, but this task has no plan yet. Call %q with your "+
			"ordered step list first (mark the step you're starting as in_progress), then "+
			"make the change. Planning before any change is required in this session.",
		call.ToolName, g.planTool))
}

// Inspect records a successful plan-tool call as satisfying the gate for the
// task. Only success counts — a rejected plan (bad args) must not unlock
// changes. Non-plan calls and failures are ignored.
func (g *PlanGuardrail) Inspect(
	ctx context.Context,
	call tools.ToolCall,
	result *tools.ToolResult,
	execErr error,
) error {
	if call.ToolName != g.planTool || !successfulResult(result, execErr) {
		return nil
	}
	id := taskscope.IDFrom(ctx)
	g.mu.Lock()
	g.planned[id] = true
	g.mu.Unlock()
	return nil
}

// ForgetTask drops the per-task plan flag for id, so a reused runner doesn't
// carry one task's plan into the next.
func (g *PlanGuardrail) ForgetTask(id taskscope.ID) {
	g.mu.Lock()
	delete(g.planned, id)
	g.mu.Unlock()
}

func (g *PlanGuardrail) isPlanned(id taskscope.ID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.planned[id]
}

// changesWorkspace reports whether a successful call to name could alter
// durable state, read from the tool's own ChangesWorkspace capability.
func (g *PlanGuardrail) changesWorkspace(ctx context.Context, name tools.ToolName) bool {
	for t := range g.iter.Tools(ctx) {
		spec := t.Definition()
		if spec.Name == name {
			return spec.ChangesWorkspace()
		}
	}
	return false
}
