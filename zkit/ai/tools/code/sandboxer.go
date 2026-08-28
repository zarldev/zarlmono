package code

import (
	"context"
	"os/exec"
)

// Sandboxer hardens a fully-prepared command just before it starts —
// the implementation may rewrite argv (re-exec shims), adjust
// SysProcAttr (namespaces), or both. Defined here, consumer-side: the
// bash tool and the process manager are the only spawners, and they
// only need this one method. The concrete implementation lives in
// zkit/agent/sandbox; anything satisfying the shape works (tests use
// in-package fakes).
//
// A Sandboxer must compose with the spawner's own setup: it is called
// after Dir, Env, stdio, and SysProcAttr (Setsid) are in place, and
// must mutate rather than replace what's already there.
type Sandboxer interface {
	Sandbox(cmd *exec.Cmd) error
}

// ContextSandboxer selects confinement using call-scoped policy such as a
// spawned task's work mode. Implementations must retain the same composition
// contract as Sandboxer.
type ContextSandboxer interface {
	SandboxContext(ctx context.Context, cmd *exec.Cmd) error
}

func sandboxCommand(ctx context.Context, sb Sandboxer, cmd *exec.Cmd) error {
	if contextual, ok := sb.(ContextSandboxer); ok {
		return contextual.SandboxContext(ctx, cmd)
	}
	return sb.Sandbox(cmd)
}
