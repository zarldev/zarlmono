// Package processenv provides compatibility helpers for constructing minimal
// child-process environments.
//
// The package intentionally avoids forwarding os.Environ wholesale. Model- or
// tool-started subprocesses should not inherit API keys, sudo helpers, cloud
// credentials, editor/session state, or other ambient secrets unless the caller
// explicitly supplies them.
//
// New code should prefer zexec.MinimalEnv directly. This package remains as a
// small stable wrapper for existing consumers.
package processenv
