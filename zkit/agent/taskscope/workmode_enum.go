package taskscope

//go:generate go tool goenums -f workmode_enum.go

// workMode is the goenums source for WorkMode — the work mode a spawned
// sub-agent run operates under, planted on the child Run's ctx so per-call
// policy layers (the shell guardrail's verify profile) can apply
// mode-specific rules without a back-channel. The trailing comment on each
// constant is the stable lowercase wire identifier (matching the spawn
// tool's work_mode argument values).
type workMode int

const (
	// none is the zero value — the run is not mode-scoped (top-level
	// runs, or a spawn with no mode set). Marked invalid so it is
	// excluded from iteration/validity while staying parseable.
	none workMode = iota // invalid none
	// explore: read-only investigation — file reads, greps, no mutation
	// and no shell.
	explore // explore
	// verify: review / sanity-check — may run tests, builds, and linters
	// via the shell, but must not mutate the workspace.
	verify // verify
	// implement: the make-changes mode — the full tool surface.
	implement // implement
)
