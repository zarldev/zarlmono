package code

// ReadOption tunes read-side tools (read, hashline read, ls, grep, glob).
type ReadOption func(*readPolicy)

type readPolicy struct {
	allowOutsideWorkspace bool
}

// WithUnrestrictedReads allows read-side tools to access paths outside the
// workspace root. Mutating tools ignore this policy and remain workspace-bound.
func WithUnrestrictedReads() ReadOption {
	return func(p *readPolicy) { p.allowOutsideWorkspace = true }
}
