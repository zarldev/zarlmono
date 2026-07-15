package shellpolicy

import "fmt"

// PolicyEngine converts a ParsedIR into a Decision. Decisions are driven
// by the IR's RiskFlags plus the engine's profile flags (see relaxed);
// the type stays a struct so future per-workspace / per-skill rules can
// slot in without changing every call site.
//
// The zero value is usable (strict profile).
type PolicyEngine struct {
	// relaxed drops the ERGONOMIC blocks — `cd` and output redirection —
	// that exist to steer the model toward workspace-aware tools rather
	// than to enforce safety. The kernel sandbox (zkit/agent/sandbox) is
	// the real filesystem boundary; when the operator turns it OFF they
	// have chosen an unconfined, high-trust mode, so these static nags no
	// longer protect anything and only provoke evasion (e.g. writing
	// files through `python3 -c`), burning iterations and tokens. When the
	// sandbox is ON these blocks stay in force as the strict profile.
	//
	// Version and syntax blocks are correctness, not nannying, so they
	// hold in both profiles. The verify profile (DecideVerify) is always
	// strict regardless of relaxed — a verify sub-agent must not mutate
	// the workspace whether or not a kernel sandbox confines it.
	relaxed bool
}

// Option configures a PolicyEngine at construction.
type Option func(*PolicyEngine)

// WithRelaxed selects the relaxed profile: the ergonomic `cd` and output
// redirect blocks step aside. Pass when the kernel sandbox is OFF. See
// PolicyEngine.relaxed.
func WithRelaxed(relaxed bool) Option {
	return func(e *PolicyEngine) { e.relaxed = relaxed }
}

// NewPolicyEngine returns a ready-to-use engine. With no options it is the
// strict profile, equivalent to the zero value.
func NewPolicyEngine(opts ...Option) *PolicyEngine {
	e := &PolicyEngine{}
	for _, o := range opts {
		o(e)
	}
	return e
}

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
// In addition, shell-side read/discovery helpers (grep, sed -n, find, ls,
// head, tail, cat, rg, and pipelines around them) are blocked with guidance to
// the registered read/search/list tools. Expansion and Subshell flags are
// informational only; the agent is meant to be capable, not nannied.
// In the relaxed profile the ergonomic `cd` and redirect blocks step aside
// (the kernel sandbox is off; see PolicyEngine.relaxed). Shell read-tool
// guidance still applies in relaxed mode because it is only a tool-routing
// correction, not a filesystem safety boundary.
func (e PolicyEngine) Decide(ir ParsedIR) Decision {
	return e.decide(ir, e.relaxed, true)
}

// decide is the shared rule body. relaxed drops the ergonomic blocks;
// callers pass e.relaxed for the standard profile and false for verify,
// which must stay strict regardless of the engine's profile. blockReadTools
// controls ergonomic steering away from shell read/discovery helpers; verify
// mode leaves that off so read-only shell diagnostics can run.
func (PolicyEngine) decide(ir ParsedIR, relaxed bool, blockReadTools bool) Decision {
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

	if blockReadTools && hasRisk(ir, ReasonShellReadTool) {
		d.IsBlocked = true
		d.BlockReason = "shell policy: use the registered workspace tools for file reading and discovery instead of shell grep/sed/find/ls/head/tail/cat or pipelines around them. " +
			"Use `program`/`grep`/`read`/`ls`/`glob`/`file_map` for workspace files; reserve bash for builds, tests, package managers, git, servers, and other real processes."
		return d
	}

	// cd and redirect are ergonomic steering, not safety. With the kernel
	// sandbox off (relaxed) they only provoke evasion, so let them pass.
	if relaxed {
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

	if hasRisk(ir, ReasonOpaqueInterpreter) {
		d.IsBlocked = true
		d.BlockReason = "shell policy: piping or heredocing code into an interpreter is blocked because static analysis cannot inspect that payload. " +
			"Use `edit`/`write` for file changes, or pass short inspectable code with interpreter -c/-e only when a real shell process is necessary."
		return d
	}

	return d
}
