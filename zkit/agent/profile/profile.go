// Package profile provides agent execution profiles — code-defined
// skeletons (model, prompt prefix, max iterations) that merge with
// persisted operator overrides at task start time.
//
// Profiles describe HOW a sub-agent behaves: which model, what its
// system prompt prefix is, how many iterations it gets. Tools come
// from the live tools.Registry; profiles do not gate them. A self-
// improving runner that builds a new tool mid-task should be able to
// call it on the next iteration without an admin step.
package profile

// Name is the identifier for a sub-agent execution profile.
type Name string

// Built-in profile names.
const (
	NameDefault    Name = "default"
	NameResearcher Name = "researcher"
	NameCoder      Name = "coder"
)

// Profile is a code-defined skeleton: persona + execution settings.
// The Resolved form merges in operator overrides and snapshots the
// live tool list.
type Profile struct {
	// Name is the identifier looked up by the registry.
	Name Name

	// Model overrides the runner's environment-default model. Empty
	// falls through to the env fallback at resolve time.
	Model string

	// PromptPrefix is prepended to the runner's system prompt to
	// shape behavior (e.g. "You are a research agent...").
	PromptPrefix string

	// MaxIterations caps the agent loop. Resolve clamps to [1, 20].
	MaxIterations int
}

// Resolved is a Profile merged with its Override, ready for the
// Runner to execute against.
type Resolved struct {
	Name          Name
	Model         string
	PromptPrefix  string
	MaxIterations int
}

// CoalesceStr returns the first non-empty string from the arguments,
// or "" if all are empty.
func CoalesceStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Deref returns *p, or the zero value of T when p is nil.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// ClampDown interprets requested <= 0 as "use limit" and clamps values
// over limit to limit itself. Limit <= 0 is invalid and returns 0.
func ClampDown(requested int32, limit int) int {
	if limit <= 0 {
		return 0
	}
	if requested <= 0 {
		return limit
	}
	r := int(requested)
	if r > limit {
		return limit
	}
	return r
}
