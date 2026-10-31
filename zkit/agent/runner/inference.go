package runner

import (
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/options"
)

// WithInferenceScope admits and accounts this runner's provider invocations in
// the supplied scope. Task totals and attempt events retain their own-only meaning.
// Compactors, planners and descendants must receive the scope at their construction.
func WithInferenceScope(scope inference.Scope) options.Option[Runner] {
	return func(r *Runner) { r.inference = scope }
}
