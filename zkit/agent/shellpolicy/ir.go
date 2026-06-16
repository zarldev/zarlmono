// Package shellpolicy parses shell commands into a compact,
// platform-neutral intermediate representation (ParsedIR) and runs a
// deterministic policy engine over it. The point of the indirection
// is that policy decisions never look at raw AST nodes — they look
// only at the small set of risk codes the IR exposes. That keeps
// decisions stable across parser upgrades and makes them trivial to
// unit-test.
//
// The package is consumed by [runner.ShellGuardrail], which converts
// Block decisions into [tools.Validation] errors before the shell
// tool runs. The agent then sees the rejection and (per the harness
// thesis) switches to the suggested better tool.
//
// Currently Unix-only. A Windows adapter (via a PowerShell bridge
// script) is intentionally deferred — zarlcode targets WSL and Linux
// dev hosts, not native Windows shells.
package shellpolicy

// IRVersion is the schema version for ParsedIR. Adapters MUST set
// this field; consumers MUST reject any payload whose version does
// not match. Bump when the IR shape changes incompatibly so stale
// persisted analyses (if any) fail closed.
const IRVersion = "1"

// Platform identifies the shell environment a ParsedIR was produced
// for. Today only Unix is implemented; the enum exists so a Windows
// adapter can slot in without churning the IR.
type Platform string

const (
	// PlatformUnix identifies IR produced by the mvdan.cc/sh-backed Unix
	// adapter — the only platform implemented today.
	PlatformUnix Platform = "unix"
)

// ReasonCode is a normalised, deterministic policy signal. Codes are
// stable across releases — add new ones rather than renaming the
// existing set. The policy engine switches on these and never reads
// raw AST nodes.
type ReasonCode string

const (
	// ReasonOperator indicates a shell control operator (|, &&, ||,
	// ;) was present. Informational on its own; combined with other
	// signals it raises confidence that the command was constructed
	// rather than a single tool invocation.
	ReasonOperator ReasonCode = "operator"

	// ReasonRedirect indicates an output redirection to a real file
	// (anything other than /dev/null and friends). Treated as Block:
	// the model should be using the write_file / edit tool, which
	// the workspace tracks and which respects the workspace root.
	ReasonRedirect ReasonCode = "redirect"

	// ReasonExpansion indicates a parameter/variable expansion
	// ($VAR, ${VAR}, $(( ))) was present. Informational; the policy
	// engine does not block on this today but the IR carries it so
	// future analyzers can act on it.
	ReasonExpansion ReasonCode = "expansion"

	// ReasonSubshell indicates a subshell or command substitution
	// ($(...), `...`, process substitution). Informational today.
	ReasonSubshell ReasonCode = "subshell"

	// ReasonCd indicates a 'cd' command was present. Blocked: bash's
	// cwd is pinned to the workspace root by design, and `cd` lets
	// the model escape the workspace boundary without intent. The
	// rejection points the model at workspace-bounded tools instead.
	ReasonCd ReasonCode = "cd"

	// ReasonSyntaxError indicates the command failed to parse. The
	// policy engine fails closed on this: the tool runs `bash -c`
	// which would surface the same error, but blocking early gives
	// the model a clearer message.
	ReasonSyntaxError ReasonCode = "syntax_error"
)

// ParsedIR is the compact, JSON-safe intermediate representation
// emitted by every adapter. It MUST NOT carry raw AST nodes — only
// the normalised signals the policy engine consults. Slice fields
// are de-duplicated and may be empty but never nil after Parse.
type ParsedIR struct {
	// Version MUST equal IRVersion; consumers MUST reject mismatches.
	Version string

	// Platform identifies which adapter produced this IR.
	Platform Platform

	// Commands lists the canonical command keys present in the parsed
	// input, in stable insertion order. For tier-2 commands (git,
	// go) the key includes the first non-flag subcommand — "git log"
	// and "git push" appear as distinct entries, which is what
	// future allow-list / risk analyzers want.
	Commands []string

	// CommandFlags maps each command key to the de-duplicated set of
	// flag tokens seen for that key. `--flag=value` is normalised to
	// `--flag`; numeric flags (-1, -20) collapse to `-*`. Useful for
	// fine-grained "git log is fine, git log --output=… is not"
	// rules without re-parsing.
	CommandFlags map[string][]string

	// Operators lists the shell control operators detected (|, &&,
	// ||, ;). De-duplicated.
	Operators []string

	// RiskFlags lists the ReasonCodes the adapter raised. The policy
	// engine switches on this to produce a Decision.
	RiskFlags []ReasonCode

	// ParseErrors carries adapter error messages when ReasonSyntaxError
	// is set. Useful for log/debug but not consulted by the engine.
	ParseErrors []string
}

// Parser is the per-platform adapter contract. Every implementation
// MUST produce a ParsedIR whose Version equals IRVersion. Parse is
// purely syntactic: it never executes the command.
type Parser interface {
	Parse(command string) (ParsedIR, error)
}

// Decision is the policy engine's output. The shell guardrail
// consumes it: IsBlocked=true converts to a Validation rejection
// whose Reason is BlockReason. ReasonCodes echo the IR's flags so
// observers can log/route on them.
type Decision struct {
	// IsBlocked is true when the command must not run. The guardrail
	// converts this to a tools.Validation error.
	IsBlocked bool

	// BlockReason is the human-readable explanation surfaced to the
	// model in the Validation message. Non-empty iff IsBlocked.
	BlockReason string

	// ReasonCodes is the (possibly empty) set of risk flags that
	// drove the decision. Always populated from the IR for
	// observability, regardless of IsBlocked.
	ReasonCodes []ReasonCode
}

// tier2Commands are the commands whose first non-flag argument is
// treated as part of the canonical command key. "git" alone is too
// coarse — "git log" is safe and "git push" is not, so the IR keeps
// them distinct. Keep this set small; broadening it widens every
// downstream analyzer's surface area.
var tier2Commands = map[string]bool{
	"git": true,
	"go":  true,
}

// hasRisk reports whether the IR carries the given risk code.
func hasRisk(ir ParsedIR, code ReasonCode) bool {
	for _, r := range ir.RiskFlags {
		if r == code {
			return true
		}
	}
	return false
}

// emptyIR returns a freshly-initialised IR with non-nil maps/slices
// so adapters never accidentally produce nil fields. All adapters
// MUST start from this rather than the zero value.
func emptyIR(p Platform) ParsedIR {
	return ParsedIR{
		Version:      IRVersion,
		Platform:     p,
		Commands:     []string{},
		CommandFlags: map[string][]string{},
		Operators:    []string{},
		RiskFlags:    []ReasonCode{},
		ParseErrors:  []string{},
	}
}
