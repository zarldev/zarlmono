package engine

import (
	"github.com/zarldev/zarlmono/zkit/options"
)

//go:generate go tool goenums -f prompt_profile.go

// promptProfile selects the embedded BUILD prompt body. The trailing comments
// are stable flag/config values.
type promptProfile int

const (
	invalid  promptProfile = iota // invalid invalid
	standard                      // standard
	lean                          // compact
)

// WithPromptProfile selects the embedded BUILD prompt. Explicit and legacy
// user overrides still take precedence during resolution.
func WithPromptProfile(profile PromptProfile) options.Option[LiveRunner] {
	return func(r *LiveRunner) {
		if profile.IsValid() {
			r.promptProfile = profile
		}
	}
}
