package guardrails_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
)

type namedGuard string

func (n namedGuard) Name() string { return string(n) }

// TestPostSchemaGuardrails_Extra pins the Extra contract: consumer-supplied
// guardrails (the zarlcode hook guardrail) compose after the full production
// set, in the order supplied.
func TestPostSchemaGuardrails_Extra(t *testing.T) {
	deps := guardrails.Deps{
		SkillLookup: stubSkillLookup{},
		Extra:       []guardrails.Guardrail{namedGuard("hooks"), namedGuard("audit")},
	}
	guards := guardrails.PostSchemaGuardrails(deps)
	if len(guards) < 2 {
		t.Fatalf("PostSchemaGuardrails returned %d guards, want at least the 2 extras", len(guards))
	}
	gotTail := []string{guards[len(guards)-2].Name(), guards[len(guards)-1].Name()}
	if gotTail[0] != "hooks" || gotTail[1] != "audit" {
		t.Errorf("chain tail = %v, want [hooks audit]", gotTail)
	}
	for _, g := range guards[:len(guards)-2] {
		if g.Name() == "hooks" || g.Name() == "audit" {
			t.Errorf("extra guardrail %q appeared inside the production set", g.Name())
		}
	}
}

// TestPostSchemaGuardrails_Disabled pins the ablation contract: Disabled
// removes guardrails by name from the assembled chain (including extras),
// unknown names are ignored, and the surviving order is unchanged.
func TestPostSchemaGuardrails_Disabled(t *testing.T) {
	deps := guardrails.Deps{
		SkillLookup: stubSkillLookup{},
		Extra:       []guardrails.Guardrail{namedGuard("hooks")},
		Disabled:    []string{"decompose", "hooks", "not-a-guardrail"},
	}
	guards := guardrails.PostSchemaGuardrails(deps)
	var names []string
	for _, g := range guards {
		names = append(names, g.Name())
	}
	for _, dropped := range []string{"decompose", "hooks"} {
		for _, n := range names {
			if n == dropped {
				t.Errorf("disabled guardrail %q survived the chain: %v", dropped, names)
			}
		}
	}
	// The rest of the production set survives in canonical order.
	want := []string{"shell_policy", "skill_hint", "fanout", "improvement_loop"}
	if len(names) != len(want) {
		t.Fatalf("chain = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("chain = %v, want %v", names, want)
		}
	}
}
