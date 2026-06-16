// Package zexec contains shared child-process mechanics: environment
// policy and process-tree lifecycle helpers. It deliberately does not
// know about tool protocols, shell heuristics, or provider-specific
// auth requirements; callers own those policies.
package zexec

import "os"

// MinimalEnv returns a small environment suitable for untrusted child
// processes. It intentionally does not forward os.Environ wholesale, so
// API keys, sudo helpers, cloud credentials, and editor/session state do
// not leak into model-started subprocesses by default.
//
// Caller-supplied env entries are appended last and therefore override
// the safe defaults when keys overlap.
func MinimalEnv(extra map[string]string) []string {
	env := make([]string, 0, 4+len(extra))
	appendIfSet := func(key string) {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}

	appendIfSet("PATH")
	// Windows process creation and DLL lookup can depend on these.
	appendIfSet("SystemRoot")
	appendIfSet("WINDIR")

	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
