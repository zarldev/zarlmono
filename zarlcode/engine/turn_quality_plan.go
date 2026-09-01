package engine

import (
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

const finalizePlanCorrection = "Have you marked the plan correctly before calling yourself complete? " +
	"If you used update_plan this turn, call update_plan once more so the plan pane matches reality: mark finished steps completed, " +
	"and if you are intentionally skipping or abandoning a step, say why in explanation. Then give your final answer."

const verifyAfterChangeCorrection = "The workspace changed after the latest successful verification. Run the narrowest relevant check for the changed code, or explain in the final answer why no executable check applies."

// FinalizePlanCorrection is the corrective prompt used for incomplete updated plans.
const FinalizePlanCorrection = finalizePlanCorrection

// planAwareTurnQuality composes the production empty-response detector with a
// zarlcode-specific completion guardrail: if the agent updated the structured
// plan during this run and then tries to finish with steps still pending or
// in_progress, inject one last correction asking it to close the plan before
// the final answer.
type planAwareTurnQuality struct {
	mu sync.Mutex

	base         runner.EmptyResponseDetector
	malformed    runner.MalformedToolCallDetector
	store        *livePlanStore
	isPlan       func() bool
	startVersion uint64
	evidence     *CompletionEvidence

	malformedCorrectionSent bool
	emptyCorrectionSent     bool
	planCorrectionSent      bool
	evidenceCorrectionSent  bool
}

// NewPlanAwareTurnQualityWithStore applies plan and completion-evidence quality
// checks against a structured plan store.
func NewPlanAwareTurnQualityWithStore(store *LivePlanStore, isPlan func() bool, evidence *CompletionEvidence) runner.TurnQuality {
	var startVersion uint64
	if store != nil {
		_, startVersion = store.Snapshot()
	}
	return &planAwareTurnQuality{
		base:         coderunner.DefaultEmptyResponseDetector(),
		malformed:    coderunner.DefaultMalformedToolCallDetector(),
		store:        store,
		isPlan:       isPlan,
		startVersion: startVersion,
		evidence:     evidence,
	}
}

func newPlanAwareTurnQuality(store *livePlanStore, isPlan func() bool, evidence ...*CompletionEvidence) runner.TurnQuality {
	var runEvidence *CompletionEvidence
	if len(evidence) > 0 {
		runEvidence = evidence[0]
	}
	return NewPlanAwareTurnQualityWithStore(store, isPlan, runEvidence)
}

func (q *planAwareTurnQuality) Inspect(content string, toolCalls []llm.ToolCall) runner.TurnQualityDecision {
	q.mu.Lock()
	defer q.mu.Unlock()

	if decision := q.inspectMalformed(content, toolCalls); decision.Correction != "" {
		return decision
	}
	if decision := q.inspectEmpty(content, toolCalls); decision.Correction != "" {
		return decision
	}
	if decision := q.inspectPlan(); decision.Correction != "" {
		return decision
	}
	return q.inspectEvidence()
}

func (q *planAwareTurnQuality) inspectPlan() runner.TurnQualityDecision {
	if q.planCorrectionSent || q.store == nil {
		return runner.TurnQualityDecision{}
	}
	if q.isPlan != nil && q.isPlan() {
		return runner.TurnQualityDecision{}
	}
	plan, version := q.store.Snapshot()
	if version <= q.startVersion || len(plan.Steps) == 0 || !planHasIncompleteSteps(plan) {
		return runner.TurnQualityDecision{}
	}
	q.planCorrectionSent = true
	return runner.TurnQualityDecision{Correction: finalizePlanCorrection}
}

func (q *planAwareTurnQuality) inspectEvidence() runner.TurnQualityDecision {
	if q.evidenceCorrectionSent || q.evidence == nil {
		return runner.TurnQualityDecision{}
	}
	if q.isPlan != nil && q.isPlan() {
		return runner.TurnQualityDecision{}
	}
	snapshot := q.evidence.Snapshot()
	if snapshot.LastMutation == 0 || !hasVerifiableCode(snapshot.MutatedPaths) {
		return runner.TurnQualityDecision{}
	}
	if snapshot.LastVerification > snapshot.LastMutation && snapshot.VerificationPassed {
		return runner.TurnQualityDecision{}
	}

	q.evidenceCorrectionSent = true
	if snapshot.LastVerification > snapshot.LastMutation && !snapshot.VerificationPassed {
		return runner.TurnQualityDecision{Correction: "The latest verification command failed: `" + snapshot.VerificationCommand + "`. Address that failure, run a narrower relevant check, or explain the unresolved failure in the final answer."}
	}
	return runner.TurnQualityDecision{Correction: verifyAfterChangeCorrection}
}

func (q *planAwareTurnQuality) inspectMalformed(content string, toolCalls []llm.ToolCall) runner.TurnQualityDecision {
	if q.malformedCorrectionSent {
		return runner.TurnQualityDecision{}
	}
	decision := q.malformed.Inspect(content, toolCalls)
	if decision.Correction == "" {
		return runner.TurnQualityDecision{}
	}
	q.malformedCorrectionSent = true
	decision.MaxCorrections = 0
	return decision
}

func (q *planAwareTurnQuality) inspectEmpty(content string, toolCalls []llm.ToolCall) runner.TurnQualityDecision {
	if q.emptyCorrectionSent {
		return runner.TurnQualityDecision{}
	}
	decision := q.base.Inspect(content, toolCalls)
	if decision.Correction == "" {
		return runner.TurnQualityDecision{}
	}
	q.emptyCorrectionSent = true
	decision.MaxCorrections = 0
	return decision
}

func planHasIncompleteSteps(plan code.Plan) bool {
	for _, step := range plan.Steps {
		if step.Status != code.StepStatuses.COMPLETED {
			return true
		}
	}
	return false
}

// PlanHasIncompleteSteps reports whether any structured plan step is unfinished.
func PlanHasIncompleteSteps(plan code.Plan) bool { return planHasIncompleteSteps(plan) }

// NewPlanAwareTurnQuality applies plan and completion-evidence quality checks.
func NewPlanAwareTurnQuality(isPlan func() bool, evidence *CompletionEvidence) runner.TurnQuality {
	return newPlanAwareTurnQuality(nil, isPlan, evidence)
}

// VerifyAfterChangeCorrection is the corrective prompt used when code changes lack verification.
const VerifyAfterChangeCorrection = verifyAfterChangeCorrection
