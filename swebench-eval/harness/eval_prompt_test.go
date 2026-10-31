package harness_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Pin the evaluated wording independently of the production constant.
const evaluationPolicyAppendix = "\n\n## Evaluation test-file policy\n\nThis is an unattended evaluation with a strict test-file policy. You may read and run existing tests, but creating or editing test files and test fixtures is blocked. Make changes to production source only; do not attempt test-file writes or use another tool to bypass that restriction. Validate with the existing tests and permitted non-test checks. Keep the public API and all enforced guardrails unchanged."

func TestEvaluationPromptTracksComposedGuardrails(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	canonical, err := harness.RenderSystemPrompt(root, registry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(canonical, "## Evaluation test-file policy") {
		t.Fatal("evaluation-only guidance leaked into canonical prompt")
	}
	arms, err := harness.AblationArms("all")
	if err != nil {
		t.Fatal(err)
	}
	for _, arm := range arms {
		t.Run(arm.Name, func(t *testing.T) {
			deps := coderunner.StandardGuardrailDeps(root, guardrails.NewTestEditStrict())
			deps.Disabled = arm.Disabled
			_, names, err := coderunner.GuardedSource(registry, deps, sourcechain.Pipeline{})
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := harness.RenderEvaluationPrompt(root, registry, names)
			if err != nil {
				t.Fatal(err)
			}
			want := canonical
			if arm.Name != "no-test-edit" {
				want += evaluationPolicyAppendix
			}
			if prompt != want {
				t.Fatalf("prompt does not match active guardrails: %v", names)
			}
		})
	}
	for _, names := range [][]string{nil, {guardrails.NewTestEditAdvisory().Name()}, {guardrails.NewTestEditStrict().Name(), guardrails.NewTestEditStrict().Name()}} {
		prompt, err := harness.RenderEvaluationPrompt(root, registry, names)
		if err != nil {
			t.Fatal(err)
		}
		want := canonical
		if len(names) == 2 {
			want += evaluationPolicyAppendix
		}
		if prompt != want {
			t.Fatalf("inactive, advisory, or duplicate guardrail handling: %v", names)
		}
	}
}
