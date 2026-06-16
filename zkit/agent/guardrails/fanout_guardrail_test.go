package guardrails_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func fanoutSuccess() *tools.ToolResult { return &tools.ToolResult{Success: true, Data: "ok"} }
func fanoutFail() *tools.ToolResult    { return &tools.ToolResult{Success: false, Error: "boom"} }

func fanoutCall(toolName, path string) tools.ToolCall {
	return tools.ToolCall{
		ID:        "c",
		ToolName:  tools.ToolName(toolName),
		Arguments: tools.ToolParameters{"path": path},
	}
}

// Under the cap: every call passes through with no rewrite.
func TestFanoutGuardrail_PassesUnderCap(t *testing.T) {
	t.Parallel()
	g := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{"read": 5})
	ctx := t.Context()
	for i := range 4 {
		if err := g.Inspect(ctx, fanoutCall("read", "file"), fanoutSuccess(), nil); err != nil {
			t.Errorf("under cap call %d: got %v, want nil", i+1, err)
		}
	}
}

// At/above the cap: Validation rejection with the spawn_agent nudge.
// The model is brute-forcing a fanout that should have been delegated.
func TestFanoutGuardrail_RejectsAtCapWithSpawnNudge(t *testing.T) {
	t.Parallel()
	g := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{"read": 3})
	ctx := t.Context()
	for range 2 {
		_ = g.Inspect(ctx, fanoutCall("read", "a"), fanoutSuccess(), nil)
	}
	err := g.Inspect(ctx, fanoutCall("read", "b"), fanoutSuccess(), nil) // 3rd
	if err == nil {
		t.Fatal("third call at cap: want Validation rejection")
	}
	var te *tools.Error
	if !errors.As(err, &te) {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if te.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want Validation", te.Kind)
	}
	for _, want := range []string{"spawn_agent", "cap 3"} {
		if !strings.Contains(te.Reason, want) {
			t.Errorf("nudge missing %q in: %q", want, te.Reason)
		}
	}
}

// Failures burn the budget too — a failing repeat-loop spends
// iterations and tokens like a successful fan-out, so it counts against
// the cap. Here two failed calls exhaust the budget of 2, and the next
// call (even a successful one) is rejected.
func TestFanoutGuardrail_FailuresBurnBudget(t *testing.T) {
	t.Parallel()
	g := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{"read": 2})
	ctx := t.Context()
	// Two failed calls — both count toward the cap of 2.
	if err := g.Inspect(ctx, fanoutCall("read", "a"), fanoutFail(), nil); err != nil {
		t.Errorf("1st failed call (under cap): got %v, want nil", err)
	}
	if err := g.Inspect(ctx, fanoutCall("read", "b"), nil, errors.New("dispatch err")); err == nil {
		t.Errorf("2nd failed call should hit cap=2, got nil")
	}
	// Budget already exhausted — a later success is rejected too.
	if err := g.Inspect(ctx, fanoutCall("read", "c"), fanoutSuccess(), nil); err == nil {
		t.Errorf("success after cap reached should be rejected, got nil")
	}
}

// Unregistered tools are unbounded — the guardrail caps only what
// the consumer asks it to cap. Bash / spawn_agent / etc. pass through.
func TestFanoutGuardrail_UnregisteredToolUnbounded(t *testing.T) {
	t.Parallel()
	g := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{"read": 1})
	ctx := t.Context()
	for i := range 100 {
		if err := g.Inspect(ctx, fanoutCall("bash", ""), fanoutSuccess(), nil); err != nil {
			t.Errorf("unregistered tool burst: got %v on call %d, want nil", err, i+1)
		}
	}
}

// Different tools have independent counters: hitting the read cap
// doesn't preempt ls calls. Mirrors the intuition that the model can
// list 19 dirs even if it's already read 30 files.
func TestFanoutGuardrail_PerToolBudgetsIndependent(t *testing.T) {
	t.Parallel()
	g := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{"read": 2, "ls": 2})
	ctx := t.Context()
	_ = g.Inspect(ctx, fanoutCall("read", "a"), fanoutSuccess(), nil)
	_ = g.Inspect(ctx, fanoutCall("read", "b"), fanoutSuccess(), nil)
	if err := g.Inspect(ctx, fanoutCall("read", "c"), fanoutSuccess(), nil); err == nil {
		t.Errorf("3rd read should hit its cap, got nil")
	}
	// ls counter is still fresh.
	if err := g.Inspect(ctx, fanoutCall("ls", "dir"), fanoutSuccess(), nil); err != nil {
		t.Errorf("1st ls after read-cap: got %v, want nil", err)
	}
}

// ForgetTask clears the per-task counter so a long-lived runner that
// serves many short tasks doesn't accumulate state.
func TestFanoutGuardrail_ForgetTaskResetsCounter(t *testing.T) {
	t.Parallel()
	g := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{"read": 2})
	ctx := t.Context()
	_ = g.Inspect(ctx, fanoutCall("read", "a"), fanoutSuccess(), nil)
	_ = g.Inspect(ctx, fanoutCall("read", "b"), fanoutSuccess(), nil)
	g.ForgetTask("") // empty TaskID is the default bucket key when ctx has none
	// Counter reset → next call should pass.
	if err := g.Inspect(ctx, fanoutCall("read", "c"), fanoutSuccess(), nil); err != nil {
		t.Errorf("after ForgetTask: got %v, want nil", err)
	}
}
