package engine_test

import (
	"strings"
	"testing"

	. "github.com/zarldev/zarlmono/zarlcode/engine"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestPlanAwareTurnQuality_IgnoresCompletedPlan(t *testing.T) {
	t.Parallel()
	store := NewLivePlanStore()
	q := NewPlanAwareTurnQualityWithStore(store, func() bool { return false }, nil)
	store.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "done", Status: code.StepStatuses.COMPLETED}}})

	if got := q.Inspect("final answer", nil); got.Correction != "" {
		t.Fatalf("completed plan should not trigger correction, got %q", got.Correction)
	}
}

func TestPlanAwareTurnQuality_InjectsWhenRunUpdatedPlanButLeftStepsOpen(t *testing.T) {
	t.Parallel()
	store := NewLivePlanStore()
	q := NewPlanAwareTurnQualityWithStore(store, func() bool { return false }, nil)
	store.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "work", Status: code.StepStatuses.INPROGRESS}}})

	got := q.Inspect("final answer", nil)
	if got.Correction == "" {
		t.Fatal("incomplete updated plan should trigger correction")
	}
	if got.Correction != FinalizePlanCorrection {
		t.Fatalf("Correction = %q, want %q", got.Correction, FinalizePlanCorrection)
	}
	if again := q.Inspect("final answer", nil); again.Correction != "" {
		t.Fatalf("plan correction should fire once, got %q on second call", again.Correction)
	}
}

func TestPlanAwareTurnQuality_IgnoresStaleIncompletePlanFromEarlierTurn(t *testing.T) {
	t.Parallel()
	store := NewLivePlanStore()
	store.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "old", Status: code.StepStatuses.PENDING}}})
	q := NewPlanAwareTurnQualityWithStore(store, func() bool { return false }, nil)

	if got := q.Inspect("final answer", nil); got.Correction != "" {
		t.Fatalf("stale inherited plan should not trigger correction, got %q", got.Correction)
	}
}

func TestPlanAwareTurnQuality_DisabledInPlanMode(t *testing.T) {
	t.Parallel()
	store := NewLivePlanStore()
	q := NewPlanAwareTurnQualityWithStore(store, func() bool { return true }, nil)
	store.SetPlan(code.Plan{Steps: []code.PlanStep{{Text: "plan step", Status: code.StepStatuses.PENDING}}})

	if got := q.Inspect("## Plan", nil); got.Correction != "" {
		t.Fatalf("plan mode should not trigger build-mode completion correction, got %q", got.Correction)
	}
}

func TestPlanAwareTurnQuality_PreservesEmptyResponseDetector(t *testing.T) {
	t.Parallel()
	store := NewLivePlanStore()
	q := NewPlanAwareTurnQualityWithStore(store, func() bool { return false }, nil)

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
			if got := PlanHasIncompleteSteps(tc.plan); got != tc.want {
				t.Fatalf("PlanHasIncompleteSteps() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanAwareTurnQuality_RequiresEvidenceAfterCodeMutation(t *testing.T) {
	t.Parallel()
	evidence := NewCompletionEvidence()
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil)
	q := NewPlanAwareTurnQualityWithStore(nil, func() bool { return false }, evidence)

	if got := q.Inspect("done", nil); got.Correction != VerifyAfterChangeCorrection {
		t.Fatalf("correction = %q, want %q", got.Correction, VerifyAfterChangeCorrection)
	}
	if again := q.Inspect("done with explanation", nil); again.Correction != "" {
		t.Fatalf("evidence correction should fire once, got %q", again.Correction)
	}
}

func TestPlanAwareTurnQuality_AcceptsPassingVerificationAfterMutation(t *testing.T) {
	t.Parallel()
	evidence := NewCompletionEvidence()
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil)
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}}, tools.Success("", "ok", tools.NewProcessEffect("go test ./pkg", 0)), nil)
	q := NewPlanAwareTurnQualityWithStore(nil, func() bool { return false }, evidence)

	if got := q.Inspect("done", nil); got.Correction != "" {
		t.Fatalf("passing verification should allow completion, got %q", got.Correction)
	}
}

func TestPlanAwareTurnQuality_ReportsFailedVerification(t *testing.T) {
	t.Parallel()
	evidence := NewCompletionEvidence()
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil)
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}}, tools.Success("", "failed", tools.NewProcessEffect("go test ./pkg", 1)), nil)
	q := NewPlanAwareTurnQualityWithStore(nil, func() bool { return false }, evidence)

	got := q.Inspect("done", nil)
	if !strings.Contains(got.Correction, "latest verification command failed") || !strings.Contains(got.Correction, "go test ./pkg") {
		t.Fatalf("failed verification correction = %q", got.Correction)
	}
}

func TestPlanAwareTurnQuality_RequiresFreshEvidenceAfterLaterMutation(t *testing.T) {
	t.Parallel()
	evidence := NewCompletionEvidence()
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil)
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}}, tools.Success("", "ok", tools.NewProcessEffect("go test ./pkg", 0)), nil)
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited again", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil)
	q := NewPlanAwareTurnQualityWithStore(nil, func() bool { return false }, evidence)

	if got := q.Inspect("done", nil); got.Correction != VerifyAfterChangeCorrection {
		t.Fatalf("stale verification correction = %q, want %q", got.Correction, VerifyAfterChangeCorrection)
	}
}

func TestPlanAwareTurnQuality_EvidenceDisabledInPlanMode(t *testing.T) {
	t.Parallel()
	evidence := NewCompletionEvidence()
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil)
	q := NewPlanAwareTurnQualityWithStore(nil, func() bool { return true }, evidence)

	if got := q.Inspect("plan", nil); got.Correction != "" {
		t.Fatalf("plan mode evidence correction = %q, want empty", got.Correction)
	}
}

func TestPlanAwareTurnQuality_DoesNotRequireEvidenceForDocsOnlyMutation(t *testing.T) {
	t.Parallel()
	evidence := NewCompletionEvidence()
	evidence.Record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "README.md")), nil)
	q := NewPlanAwareTurnQualityWithStore(nil, func() bool { return false }, evidence)

	if got := q.Inspect("done", nil); got.Correction != "" {
		t.Fatalf("docs-only mutation should allow completion, got %q", got.Correction)
	}
}
