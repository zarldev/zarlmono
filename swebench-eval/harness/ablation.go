package harness

import (
	"fmt"
	"strings"
)

// Ablation names one guardrail-chain variant for an A/B eval run. The
// zero value is the baseline (full production chain, deterministic
// decompose path) — every other arm drops one guardrail or arms the
// constrained-verdict judge, so a run over several arms isolates each
// mechanism's contribution to resolve rate.
type Ablation struct {
	// Name labels the arm; it is appended to the driver name (and thus
	// the eval_results natural key), so arms group as distinct drivers
	// in the report with no schema changes.
	Name string
	// Disabled lists guardrail names removed from the production chain
	// (guardrails.Deps.Disabled).
	Disabled []string
	// Judge arms the constrained-verdict decompose judge on the run's
	// own provider (the production default keeps the deterministic
	// advisory path).
	Judge bool
}

// ablationArms is the canonical arm set, one per guardrail mechanism.
// Names here are report labels; Disabled entries are guardrail Name()
// values and must match the chain (composition_golden_test pins them).
var ablationArms = []Ablation{
	{Name: "baseline"},
	{Name: "no-shell", Disabled: []string{"shell_policy"}},
	{Name: "no-skill-hint", Disabled: []string{"skill_hint"}},
	{Name: "no-decompose", Disabled: []string{"decompose"}},
	{Name: "no-fanout", Disabled: []string{"fanout"}},
	{Name: "no-test-edit", Disabled: []string{"test_edit_strict"}},
	{Name: "no-improvement", Disabled: []string{"improvement_loop"}},
	{Name: "judge", Judge: true},
}

// AblationArms resolves a comma-separated arm spec ("baseline,no-decompose"),
// or "all" for the full canonical set. An unknown arm name is an error — a
// typo'd arm silently running as baseline would corrupt the comparison.
func AblationArms(spec string) ([]Ablation, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	if spec == "all" {
		return append([]Ablation(nil), ablationArms...), nil
	}
	byName := make(map[string]Ablation, len(ablationArms))
	for _, a := range ablationArms {
		byName[a.Name] = a
	}
	var out []Ablation
	for _, raw := range strings.Split(spec, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		a, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown ablation arm %q (known: %s)", name, strings.Join(ablationNames(), ", "))
		}
		out = append(out, a)
	}
	return out, nil
}

func ablationNames() []string {
	out := make([]string, 0, len(ablationArms))
	for _, a := range ablationArms {
		out = append(out, a.Name)
	}
	return out
}
