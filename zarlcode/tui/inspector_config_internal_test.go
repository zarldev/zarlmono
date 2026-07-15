package tui

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// The inspector's guardrail summary is derived from the real Deps, not a
// hand-maintained string: optional verifiers, fan-out caps, and test-edit policy
// all come from the struct, so changing a limit changes the summary.
func TestGuardrailSummary_ReflectsDeps(t *testing.T) {
	deps := guardrails.Deps{
		Verifiers: []guardrails.Verifier{&guardrails.GoVerifier{}},
		FanoutLimits: map[tools.ToolName]int{
			code.ToolNameGrep: 30,
		},
		TestEdit: guardrails.NewTestEditAdvisory(),
	}
	got := guardrailSummary(deps)

	for _, want := range []string{"go_verifier", ".go", "grep≤30", "test_edit_advisory"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "read≤") {
		t.Errorf("read should not have a fan-out limit in the summary:\n%s", got)
	}
}

func TestGuardrailSummary_OmitsTestEditWhenNil(t *testing.T) {
	deps := guardrails.Deps{
		FanoutLimits: map[tools.ToolName]int{code.ToolNameGrep: 30},
		// TestEdit left nil — interactive mode behaviour.
	}
	got := guardrailSummary(deps)
	if strings.Contains(got, "test_edit") {
		t.Errorf("nil TestEdit should be omitted from summary, got:\n%s", got)
	}
	if !strings.Contains(got, "grep≤30") {
		t.Errorf("summary should still include fanout, got:\n%s", got)
	}
}

// The fan-out number is read from the map — a different limit yields a
// different summary, proving the value isn't hardcoded.
func TestGuardrailSummary_DerivesFanoutValue(t *testing.T) {
	deps := guardrails.Deps{
		FanoutLimits: map[tools.ToolName]int{code.ToolNameGrep: 99},
	}
	if got := guardrailSummary(deps); !strings.Contains(got, "grep≤99") {
		t.Errorf("summary should derive the fan-out value from the map, got:\n%s", got)
	}
}

// Empty config renders a clear sentinel rather than a blank panel.
func TestGuardrailSummary_EmptySentinel(t *testing.T) {
	if got := guardrailSummary(guardrails.Deps{}); got != "(none configured)" {
		t.Errorf("empty deps should render a sentinel, got %q", got)
	}
}
