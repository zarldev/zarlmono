package tools

//go:generate go tool goenums -f kind_enum.go

// kind is the goenums source for Kind — the tool-error classification the
// runner, guardrails, and agents switch on instead of parsing error
// strings. The trailing comment on each constant is the stable lowercase
// wire/log identifier; the leading comment is the semantic contract.
type kind int

const (
	// unknown is the zero value — no classification applies (legacy
	// errors, unwrapped third-party failures). Marked invalid so it is
	// excluded from iteration/validity while staying parseable.
	unknown kind = iota // invalid unknown
	// validation: the caller passed malformed arguments (missing field,
	// wrong type, out-of-range). Retrying the same args won't help.
	validation // validation
	// notFound: a requested resource (file, process, tool name) doesn't
	// exist. Retrying the same target won't help.
	notFound // not_found
	// permission: the operation isn't permitted under the current sandbox
	// / filesystem ACL / auth context. Hard fail.
	permission // permission
	// transient: a temporary failure (network blip, locked resource, rate
	// limit). Safe to retry, possibly with backoff.
	transient // transient
	// budget: a per-task budget (calls, bytes, wall time) was exhausted.
	// Retry only after the budget is raised.
	budget // budget
	// fatal: a non-recoverable execution failure (panic, corrupt state,
	// programming bug). Don't retry.
	fatal // fatal
	// stale: the arguments were well-formed but the target moved under
	// them (a line/hash anchor no longer matches because the file changed
	// since it was read). The fix is to re-read the target and retry with
	// fresh anchors — not to "fix the format". Distinct from validation so
	// guardrails advise re-reading instead of blaming the input shape.
	stale // stale
)
