package guardrails

import (
	"slices"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Deps is the zarlcode production guardrail configuration. It is a
// single-consumer config, not a general guardrail catalog: mandatory fields
// configure concrete guardrails, DecomposeJudge optionally overrides the
// decompose advisory path, and TestEdit fills the fixed test-edit slot when
// non-nil.
type Deps struct {
	// Mandatory leaf config for specific guardrails.
	SkillLookup         SkillLookup
	Verifiers           []Verifier
	WorkspaceRoot       string
	FanoutLimits        map[tools.ToolName]int
	ReadBeforeWriteMode ReadBeforeWriteMode
	ExtraEvidence       TaskCallLedger

	// ShellLenient relaxes the static guardrails that exist to steer the
	// model rather than to enforce safety: the shell policy's ergonomic
	// cd/redirect blocks step aside and a strict read-before-write mode is
	// softened to advisory. The zarlcode engine sets it when the kernel
	// sandbox setting is OFF — with no kernel boundary the operator has
	// chosen an unconfined, high-trust mode, so these blocks only provoke
	// evasion (file writes through `python3 -c`) and burn tokens. Off
	// (strict) by default; the sandbox-on path keeps every block in force.
	ShellLenient bool

	// Optional override. Nil keeps the decompose guardrail's deterministic path.
	DecomposeJudge VerdictJudge

	// PlanFirst arms the plan-first gate: the first workspace-changing call in
	// a task is refused until the planning tool has run. Off by default; the
	// local-model profile turns it on to stop weak models diving into edits
	// before planning. The gate needs the spec Iterable, so sourcechain.New
	// composes it (next to schema) rather than PostSchemaGuardrails.
	PlanFirst bool

	// PlanTool names the tool whose success satisfies PlanFirst. Zero value
	// means the gate is inert even when PlanFirst is set (no tool can clear
	// it), so a consumer enabling PlanFirst must set this.
	PlanTool tools.ToolName

	// Optional slot fill. The caller chooses advisory vs strict; this package
	// owns the canonical production slot after fanout and before improvement.
	TestEdit Guardrail

	// Extra appends consumer-supplied guardrails after the production set —
	// the zarlcode engine arms user-defined command hooks here. They run
	// last so user hooks only see calls the production chain already
	// admitted. Nil entries are rejected at chain build time.
	Extra []Guardrail

	// Disabled removes guardrails from the composed chain by Name() — the
	// eval harness's ablation knob, so an A/B arm can drop one guardrail
	// while the rest keep their canonical order. Filtering happens after
	// the full set (including TestEdit and Extra) is assembled; unknown
	// names are ignored. Production consumers leave it nil. The schema
	// guardrail is not part of this set — it is composed by sourcechain
	// as a correctness layer, not a policy choice.
	Disabled []string
}

// PostSchemaGuardrails returns the production guardrails that compose after
// schema, in order. "Post-schema" is the chain position; the returned set mixes
// PreCall and PostCall guardrails.
func PostSchemaGuardrails(deps Deps) []Guardrail {
	decompose := NewDecomposeGuardrail(0)
	if deps.DecomposeJudge != nil {
		decompose = decompose.WithJudge(deps.DecomposeJudge)
	}

	guards := []Guardrail{
		NewShellGuardrail(code.ToolNameBash, WithShellLenient(deps.ShellLenient)),
		NewSkillHintGuardrail(deps.SkillLookup),
		decompose,
		NewFanoutGuardrail(deps.FanoutLimits),
	}
	// With the sandbox off, soften a strict read-before-write to advisory:
	// strict mode blocks edit/write tool calls, which only pushes the model
	// to write through bash/python where this guardrail can't see it.
	rbwMode := deps.ReadBeforeWriteMode
	if deps.ShellLenient && rbwMode == ReadBeforeWriteStrict {
		rbwMode = ReadBeforeWriteAdvisory
	}
	if rbw := NewReadBeforeWriteGuardrail(deps.ExtraEvidence, rbwMode); rbw != nil {
		guards = append(guards, rbw)
	}
	if deps.TestEdit != nil {
		guards = append(guards, deps.TestEdit)
	}
	guards = append(guards, NewImprovementGuardrail(deps.WorkspaceRoot, nil, deps.Verifiers...))
	guards = append(guards, deps.Extra...)
	if len(deps.Disabled) > 0 {
		drop := make(map[string]struct{}, len(deps.Disabled))
		for _, name := range deps.Disabled {
			drop[name] = struct{}{}
		}
		guards = slices.DeleteFunc(guards, func(g Guardrail) bool {
			_, ok := drop[g.Name()]
			return ok
		})
	}
	return guards
}
