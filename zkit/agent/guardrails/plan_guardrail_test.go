package guardrails_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const planToolName tools.ToolName = "update_plan"

// planIter backs the guardrail with three tools: a read-only one, a
// file-mutating one, and a shell tool that only sets AffectsWorkspace — so
// the gate's ChangesWorkspace classification is exercised on all three.
func planIter() tools.Iterable {
	return stubIter{all: []tools.Tool{
		specTool{spec: tools.ToolSpec{Name: "read"}},
		specTool{spec: tools.ToolSpec{Name: "edit", Mutates: true}},
		specTool{spec: tools.ToolSpec{Name: "bash", AffectsWorkspace: true}},
		specTool{spec: tools.ToolSpec{Name: planToolName}},
	}}
}

func planCall(name tools.ToolName) tools.ToolCall {
	return tools.ToolCall{ID: "c", ToolName: name}
}

func planSuccess() *tools.ToolResult { return &tools.ToolResult{Success: true} }

// A read-only call never needs a plan — it must pass before any planning.
func TestPlanGuardrail_AllowsReadsWithoutPlan(t *testing.T) {
	t.Parallel()
	g := guardrails.NewPlanGuardrail(planIter(), planToolName)
	ctx := taskscope.WithID(t.Context(), "task-1")
	if err := g.Before(ctx, planCall("read")); err != nil {
		t.Fatalf("read before plan: got %v, want nil", err)
	}
}

// The planning tool itself must always be callable — otherwise the gate
// could never be satisfied.
func TestPlanGuardrail_AllowsPlanToolWithoutPlan(t *testing.T) {
	t.Parallel()
	g := guardrails.NewPlanGuardrail(planIter(), planToolName)
	ctx := taskscope.WithID(t.Context(), "task-1")
	if err := g.Before(ctx, planCall(planToolName)); err != nil {
		t.Fatalf("plan tool before plan: got %v, want nil", err)
	}
}

// A file edit and a bash command are both refused before a plan exists; the
// rejection is a Validation error naming the planning tool.
func TestPlanGuardrail_BlocksChangesBeforePlan(t *testing.T) {
	t.Parallel()
	for _, name := range []tools.ToolName{"edit", "bash"} {
		t.Run(name.String(), func(t *testing.T) {
			t.Parallel()
			g := guardrails.NewPlanGuardrail(planIter(), planToolName)
			ctx := taskscope.WithID(t.Context(), "task-1")
			err := g.Before(ctx, planCall(name))
			if err == nil {
				t.Fatalf("%s before plan: want rejection", name)
			}
			var te *tools.Error
			if !errors.As(err, &te) {
				t.Fatalf("err is %T, want *tools.Error", err)
			}
			if te.Kind != tools.Kinds.VALIDATION {
				t.Errorf("Kind = %v, want Validation", te.Kind)
			}
		})
	}
}

// After a successful plan-tool call, changing tools are admitted.
func TestPlanGuardrail_AllowsChangesAfterPlan(t *testing.T) {
	t.Parallel()
	g := guardrails.NewPlanGuardrail(planIter(), planToolName)
	ctx := taskscope.WithID(t.Context(), "task-1")
	if err := g.Inspect(ctx, planCall(planToolName), planSuccess(), nil); err != nil {
		t.Fatalf("inspect plan success: %v", err)
	}
	if err := g.Before(ctx, planCall("edit")); err != nil {
		t.Errorf("edit after plan: got %v, want nil", err)
	}
	if err := g.Before(ctx, planCall("bash")); err != nil {
		t.Errorf("bash after plan: got %v, want nil", err)
	}
}

// A FAILED plan-tool call must not satisfy the gate — a rejected plan is no
// plan.
func TestPlanGuardrail_FailedPlanDoesNotUnlock(t *testing.T) {
	t.Parallel()
	g := guardrails.NewPlanGuardrail(planIter(), planToolName)
	ctx := taskscope.WithID(t.Context(), "task-1")
	failed := &tools.ToolResult{Success: false, Error: "bad plan"}
	if err := g.Inspect(ctx, planCall(planToolName), failed, nil); err != nil {
		t.Fatalf("inspect plan failure: %v", err)
	}
	if err := g.Before(ctx, planCall("edit")); err == nil {
		t.Error("edit after failed plan: want rejection")
	}
}

// The plan flag is per-task: planning in one task does not unlock another.
func TestPlanGuardrail_PlanIsPerTask(t *testing.T) {
	t.Parallel()
	g := guardrails.NewPlanGuardrail(planIter(), planToolName)
	planned := taskscope.WithID(t.Context(), "task-1")
	other := taskscope.WithID(t.Context(), "task-2")
	if err := g.Inspect(planned, planCall(planToolName), planSuccess(), nil); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if err := g.Before(other, planCall("edit")); err == nil {
		t.Error("edit in unplanned task: want rejection")
	}
}

// ForgetTask drops the plan flag so a reused task ID re-arms the gate.
func TestPlanGuardrail_ForgetTaskReArms(t *testing.T) {
	t.Parallel()
	g := guardrails.NewPlanGuardrail(planIter(), planToolName)
	ctx := taskscope.WithID(t.Context(), "task-1")
	_ = g.Inspect(ctx, planCall(planToolName), planSuccess(), nil)
	g.ForgetTask("task-1")
	if err := g.Before(ctx, planCall("edit")); err == nil {
		t.Error("edit after ForgetTask: want rejection (plan flag should be cleared)")
	}
}
