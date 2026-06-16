package processenv

import "github.com/zarldev/zarlmono/zkit/zexec"

// Minimal returns a small environment suitable for untrusted child
// processes. It intentionally does not forward os.Environ wholesale, so
// API keys, sudo helpers, cloud credentials, and editor/session state do
// not leak into model-started subprocesses by default.
//
// Caller-supplied env entries are appended last and therefore override
// the safe defaults when keys overlap.
//
// Minimal is kept as a compatibility wrapper around [zexec.MinimalEnv].
// New child-process code should import zkit/zexec directly.
func Minimal(extra map[string]string) []string {
	return zexec.MinimalEnv(extra)
}
