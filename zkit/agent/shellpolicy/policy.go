package shellpolicy

import "fmt"

// PolicyEngine converts a ParsedIR into a Decision. The engine has
// no fields today — decisions are driven entirely by the IR's
// RiskFlags — but the type stays a struct so a future allow-list
// (per-workspace overrides, per-skill rules) can slot in without
// changing every call site.
//
// The zero value is usable.
type PolicyEngine struct{}

// NewPolicyEngine returns a ready-to-use engine. The zero value is
// equivalent; the constructor exists for symmetry.
func NewPolicyEngine() *PolicyEngine { return &PolicyEngine{} }

// Decide converts an IR into a Decision. Block rules are evaluated
// in priority order; the first match wins. ReasonCodes always echo
// the IR's RiskFlags so callers can observe the full picture even
// on pass.
//
// Block rules (in order):
//
//  1. Version mismatch — fail closed. Should never happen in a
//     single-binary build but cheap to defend against.
//  2. Syntax error — the command wouldn't run anyway; surface a
//     clear "your command didn't parse" message instead of letting
//     bash complain mid-execution.
//  3. cd — bash's cwd is the workspace root by design; cd is a
//     boundary-escape vector. Redirect the model to workspace tools.
//  4. Unsafe output redirect — there's already a write_file / edit
//     tool that respects the workspace; the model should use that.
//
// Anything else passes. Operator / Expansion / Subshell flags are
// informational only; the agent is meant to be capable, not nannied.
func (PolicyEngine) Decide(ir ParsedIR) Decision {
	d := Decision{ReasonCodes: append([]ReasonCode(nil), ir.RiskFlags...)}

	if ir.Version != IRVersion {
		d.IsBlocked = true
		d.BlockReason = fmt.Sprintf(
			"shell policy: IR version %q does not match expected %q — refusing to run a command whose analysis cannot be trusted",
			ir.Version,
			IRVersion,
		)
		return d
	}

	if hasRisk(ir, ReasonSyntaxError) {
		d.IsBlocked = true
		d.BlockReason = "shell policy: command did not parse as a Unix shell statement; fix the syntax and retry"
		if len(ir.ParseErrors) > 0 {
			d.BlockReason += " (parser said: " + ir.ParseErrors[0] + ")"
		}
		return d
	}

	if hasRisk(ir, ReasonCd) {
		d.IsBlocked = true
		d.BlockReason = "shell policy: `cd` is blocked because bash already runs at the workspace root. " +
			"Use workspace-aware tools (read, write, edit, ls, grep) for paths inside the workspace, " +
			"or invoke the binary directly with an absolute path if you really need to act outside it"
		return d
	}

	if hasRisk(ir, ReasonRedirect) {
		d.IsBlocked = true
		d.BlockReason = "shell policy: output redirection to a file is blocked. " +
			"Use the `write` tool (creates a new file), `write_append` (appends to an existing one), " +
			"or `edit` (in-place replacement) so the workspace tracks the change. " +
			"Redirect to /dev/null is fine if you only want to drop output."
		return d
	}

	return d
}
