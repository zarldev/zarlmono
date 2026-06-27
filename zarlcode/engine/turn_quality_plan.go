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

	malformedCorrectionSent bool
	emptyCorrectionSent     bool
	planCorrectionSent      bool
}

func newPlanAwareTurnQuality(store *livePlanStore, isPlan func() bool) runner.TurnQuality {
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
	}
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
