package code_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// recordingStore is a PlanStore that captures every SetPlan call.
// Lets tests assert both the final state and the call sequence
// (multiple update_plan invocations across plan/build mode flows).
type recordingStore struct {
	plans []code.Plan
}

func (r *recordingStore) SetPlan(p code.Plan) { r.plans = append(r.plans, p) }
func (r *recordingStore) GetPlan() code.Plan {
	if len(r.plans) == 0 {
		return code.Plan{}
	}
	return r.plans[len(r.plans)-1]
}

func runUpdatePlan(t *testing.T, store code.PlanStore, args code.UpdatePlanArgs) *tools.ToolResult {
	t.Helper()
	return execTyped(t, code.NewUpdatePlanTool(store), args)
}

func TestUpdatePlan_SeedsPendingList(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	res := runUpdatePlan(t, store, code.UpdatePlanArgs{
		Plan: []code.UpdatePlanStepArg{
			{Step: "Add Foo field", Status: "pending"},
			{Step: "Update Marshal", Status: "pending"},
			{Step: "Add test", Status: "pending"},
		},
		Explanation: "seeded from plan-mode output",
	})
	if !res.Success {
		t.Fatalf("expected success: %s", res.Error)
	}
	p := store.GetPlan()
	if len(p.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(p.Steps))
	}
	if p.Steps[0].Text != "Add Foo field" || p.Steps[0].Status != code.StepStatuses.PENDING {
		t.Errorf("first step wrong: %+v", p.Steps[0])
	}
	if p.Explanation != "seeded from plan-mode output" {
		t.Errorf("explanation = %q", p.Explanation)
	}
}

func TestUpdatePlan_AcceptsStatusTransitions(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}

	// Seed
	res := runUpdatePlan(t, store, code.UpdatePlanArgs{Plan: []code.UpdatePlanStepArg{
		{Step: "A", Status: "pending"},
		{Step: "B", Status: "pending"},
	}})
	if !res.Success {
		t.Fatalf("seed: %s", res.Error)
	}

	// Start A
	res = runUpdatePlan(t, store, code.UpdatePlanArgs{Plan: []code.UpdatePlanStepArg{
		{Step: "A", Status: "in_progress"},
		{Step: "B", Status: "pending"},
	}})
	if !res.Success {
		t.Fatalf("start A: %s", res.Error)
	}

	// Finish A, start B
	res = runUpdatePlan(t, store, code.UpdatePlanArgs{Plan: []code.UpdatePlanStepArg{
		{Step: "A", Status: "completed"},
		{Step: "B", Status: "in_progress"},
	}})
	if !res.Success {
		t.Fatalf("transition: %s", res.Error)
	}
	data, _ := res.Data.(string)
	if !strings.Contains(data, "1 completed") {
		t.Errorf("summary missing counts: %q", data)
	}

	if len(store.plans) != 3 {
		t.Errorf("expected 3 SetPlan calls, got %d", len(store.plans))
	}
	final := store.GetPlan()
	if final.Steps[0].Status != code.StepStatuses.COMPLETED {
		t.Errorf("A final status = %v", final.Steps[0].Status)
	}
	if final.Steps[1].Status != code.StepStatuses.INPROGRESS {
		t.Errorf("B final status = %v", final.Steps[1].Status)
	}
}

func TestUpdatePlan_RejectsInvalidShape(t *testing.T) {
	t.Parallel()
	// Shape errors that REMAIN in Execute after the typed-args
	// migration: missing/empty plan, empty step text, unknown status.
	// The previous map-based cases ("plan not array", "step not object")
	// are no longer reachable — they fail at the tools.DecodeArgs
	// JSON-roundtrip boundary before the rest of Execute runs. That
	// path is covered in pkg/ai/tools/typed_test.go.
	tests := []struct {
		name string
		args code.UpdatePlanArgs
		want string
	}{
		{
			name: "missing plan",
			args: code.UpdatePlanArgs{},
			want: "plan array required",
		},
		{
			name: "empty plan",
			args: code.UpdatePlanArgs{Plan: []code.UpdatePlanStepArg{}},
			want: "plan array required",
		},
		{
			name: "step missing text",
			args: code.UpdatePlanArgs{Plan: []code.UpdatePlanStepArg{
				{Step: "", Status: "pending"},
			}},
			want: "empty `step`",
		},
		{
			name: "bad status",
			args: code.UpdatePlanArgs{Plan: []code.UpdatePlanStepArg{
				{Step: "do thing", Status: "nope"},
			}},
			want: "unknown step status",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := runUpdatePlan(t, &recordingStore{}, tt.args)
			if res.Success {
				t.Errorf("expected failure for case %q", tt.name)
			}
			if !strings.Contains(res.Error, tt.want) {
				t.Errorf("err = %q, want substring %q", res.Error, tt.want)
			}
		})
	}
}

func TestStepStatus_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s    code.StepStatus
		wire string
	}{
		{code.StepStatuses.PENDING, `"pending"`},
		{code.StepStatuses.INPROGRESS, `"in_progress"`},
		{code.StepStatuses.COMPLETED, `"completed"`},
	}
	for _, tt := range tests {
		t.Run(tt.wire, func(t *testing.T) {
			t.Parallel()
			out, err := json.Marshal(tt.s)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != tt.wire {
				t.Errorf("marshalled = %s, want %s", out, tt.wire)
			}
			var back code.StepStatus
			if err := json.Unmarshal(out, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if back != tt.s {
				t.Errorf("round-trip = %v, want %v", back, tt.s)
			}
		})
	}
}

// TestParseStepStatus_Aliases covers the wire form plus the aliases baked
// into the goenums source comment. Case folding, whitespace trimming, and
// empty -> pending are the caller's responsibility (see
// TestUpdatePlan_NormalizesStatus), not the generated parser's.
func TestParseStepStatus_Aliases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want code.StepStatus
		err  bool
	}{
		{"pending", code.StepStatuses.PENDING, false},
		{"in_progress", code.StepStatuses.INPROGRESS, false},
		{"in-progress", code.StepStatuses.INPROGRESS, false},
		{"inprogress", code.StepStatuses.INPROGRESS, false},
		{"completed", code.StepStatuses.COMPLETED, false},
		{"done", code.StepStatuses.COMPLETED, false},
		{"bogus", code.StepStatuses.PENDING, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := code.ParseStepStatus(tt.in)
			if (err != nil) != tt.err {
				t.Errorf("err = %v, want err=%v", err, tt.err)
			}
			if !tt.err && got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestUpdatePlan_NormalizesStatus asserts the tool tolerates raw model
// output — surrounding whitespace, mixed case, and an omitted status
// (treated as pending) — by normalising before parsing.
func TestUpdatePlan_NormalizesStatus(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	res := runUpdatePlan(t, store, code.UpdatePlanArgs{
		Plan: []code.UpdatePlanStepArg{
			{Step: "a", Status: "  In_Progress "},
			{Step: "b", Status: "DONE"},
			{Step: "c", Status: ""},
		},
	})
	if !res.Success {
		t.Fatalf("expected success: %s", res.Error)
	}
	p := store.GetPlan()
	want := []code.StepStatus{code.StepStatuses.INPROGRESS, code.StepStatuses.COMPLETED, code.StepStatuses.PENDING}
	for i, w := range want {
		if p.Steps[i].Status != w {
			t.Errorf("step %d status = %v, want %v", i, p.Steps[i].Status, w)
		}
	}
}

func TestMemoryPlanStore_RoundTrip(t *testing.T) {
	t.Parallel()
	s := code.NewMemoryPlanStore()
	if !s.GetPlan().IsEmpty() {
		t.Errorf("fresh store should be empty")
	}
	s.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "x", Status: code.StepStatuses.PENDING}}})
	if s.GetPlan().IsEmpty() {
		t.Errorf("after SetPlan, should not be empty")
	}
}
