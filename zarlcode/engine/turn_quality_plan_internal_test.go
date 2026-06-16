package engine

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestPlanAwareTurnQuality_IgnoresCompletedPlan(t *testing.T) {
	t.Parallel()
	store := &livePlanStore{}
	q, ok := newPlanAwareTurnQuality(store, func() bool { return false }).(*planAwareTurnQuality)
	if !ok {
		t.Fatal("newPlanAwareTurnQuality should return *planAwareTurnQuality")
	}
	store.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "done", Status: code.StepStatuses.COMPLETED}}})

	if got := q.Inspect("final answer", nil); got.Correction != "" {
		t.Fatalf("completed plan should not trigger correction, got %q", got.Correction)
	}
}

func TestPlanAwareTurnQuality_InjectsWhenRunUpdatedPlanButLeftStepsOpen(t *testing.T) {
	t.Parallel()
	store := &livePlanStore{}
	q, ok := newPlanAwareTurnQuality(store, func() bool { return false }).(*planAwareTurnQuality)
	if !ok {
		t.Fatal("newPlanAwareTurnQuality should return *planAwareTurnQuality")
	}
	store.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "work", Status: code.StepStatuses.INPROGRESS}}})

	got := q.Inspect("final answer", nil)
	if got.Correction == "" {
		t.Fatal("incomplete updated plan should trigger correction")
	}
	if got.Correction != finalizePlanCorrection {
		t.Fatalf("Correction = %q, want %q", got.Correction, finalizePlanCorrection)
	}
	if again := q.Inspect("final answer", nil); again.Correction != "" {
		t.Fatalf("plan correction should fire once, got %q on second call", again.Correction)
	}
}

func TestPlanAwareTurnQuality_IgnoresStaleIncompletePlanFromEarlierTurn(t *testing.T) {
	t.Parallel()
	store := &livePlanStore{}
	store.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "old", Status: code.StepStatuses.PENDING}}})
	q, ok := newPlanAwareTurnQuality(store, func() bool { return false }).(*planAwareTurnQuality)
	if !ok {
		t.Fatal("newPlanAwareTurnQuality should return *planAwareTurnQuality")
	}

	if got := q.Inspect("final answer", nil); got.Correction != "" {
		t.Fatalf("stale inherited plan should not trigger correction, got %q", got.Correction)
	}
}

func TestPlanAwareTurnQuality_DisabledInPlanMode(t *testing.T) {
	t.Parallel()
	store := &livePlanStore{}
	q, ok := newPlanAwareTurnQuality(store, func() bool { return true }).(*planAwareTurnQuality)
	if !ok {
		t.Fatal("newPlanAwareTurnQuality should return *planAwareTurnQuality")
	}
	store.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "plan step", Status: code.StepStatuses.PENDING}}})

	if got := q.Inspect("## Plan", nil); got.Correction != "" {
		t.Fatalf("plan mode should not trigger build-mode completion correction, got %q", got.Correction)
	}
}

func TestPlanAwareTurnQuality_PreservesEmptyResponseDetector(t *testing.T) {
	t.Parallel()
	store := &livePlanStore{}
	q, ok := newPlanAwareTurnQuality(store, func() bool { return false }).(*planAwareTurnQuality)
	if !ok {
		t.Fatal("newPlanAwareTurnQuality should return *planAwareTurnQuality")
	}

	got := q.Inspect("", nil)
	if got.Correction == "" {
		t.Fatal("empty response should still trigger correction")
	}
	if got.Correction != coderunner.DefaultEmptyResponseDetector().Inspect("", nil).Correction {
		t.Fatalf("empty correction = %q, want production empty-response correction", got.Correction)
	}
	if !got.DisableThinking {
		t.Fatal("empty-response retry should still disable thinking")
	}
	if got.MaxCorrections != 0 {
		t.Fatalf("wrapper should clear MaxCorrections after latching, got %d", got.MaxCorrections)
	}
	if again := q.Inspect("", nil); again.Correction != "" {
		t.Fatalf("empty correction should fire once, got %q on second call", again.Correction)
	}
}

func TestPlanHasIncompleteSteps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		plan code.Plan
		want bool
	}{
		{name: "empty", plan: code.Plan{}, want: false},
		{name: "all completed", plan: code.Plan{Steps: []code.PlanStep{{Text: "done", Status: code.StepStatuses.COMPLETED}}}, want: false},
		{name: "pending", plan: code.Plan{Steps: []code.PlanStep{{Text: "todo", Status: code.StepStatuses.PENDING}}}, want: true},
		{name: "in progress", plan: code.Plan{Steps: []code.PlanStep{{Text: "doing", Status: code.StepStatuses.INPROGRESS}}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := planHasIncompleteSteps(tc.plan); got != tc.want {
				t.Fatalf("planHasIncompleteSteps() = %v, want %v", got, tc.want)
			}
		})
	}
}

var _ runner.TurnQuality = (*planAwareTurnQuality)(nil)
