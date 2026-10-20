package harness

import (
	"slices"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// EvaluationPromptRevision identifies the evaluator's prompt-composition policy
// in run manifests. It does not imply that every ablation enables test protection.
const EvaluationPromptRevision = "test-file-policy-v1"

const evaluationTestFileGuidance = "This is an unattended evaluation with a strict test-file policy. You may read and run existing tests, but creating or editing test files and test fixtures is blocked. Make changes to production source only; do not attempt test-file writes or use another tool to bypass that restriction. Validate with the existing tests and permitted non-test checks. Keep the public API and all enforced guardrails unchanged."

// RenderEvaluationPrompt adds evaluation-only guidance to the shared canonical
// prompt when the composed tool chain includes strict test-edit protection.
// activeGuardrails is borrowed for this call. Disabling that guardrail omits the
// appendix; the canonical renderer and interactive prompt remain unchanged.
func RenderEvaluationPrompt(workspaceRoot string, reg *tools.Registry, activeGuardrails []string) (string, error) {
	prompt, err := RenderSystemPrompt(workspaceRoot, reg)
	if err != nil {
		return "", err
	}
	if slices.Contains(activeGuardrails, guardrails.NewTestEditStrict().Name()) {
		prompt += "\n\n## Evaluation test-file policy\n\n" + evaluationTestFileGuidance
	}
	return prompt, nil
}
