package engine

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// ShellSandbox establishes both normal and verify confinement when enabled.
// build is the platform sandbox constructor; an explicit disable is the only
// path that returns no sandbox without an error.
func ShellSandbox(enabled bool, root string, policy sandbox.Policy, build func(sandbox.Policy) (*sandbox.Sandbox, error)) (code.Sandboxer, error) {
	if !enabled {
		return nil, nil //nolint:nilnil // Explicit disable selects unconfined execution, not a setup failure.
	}
	normal, err := build(policy)
	if err != nil {
		return nil, fmt.Errorf("shell sandbox unavailable (explicitly disable shell_sandbox or set ZARLCODE_SANDBOX=0 to run unconfined): %w", err)
	}
	verify, err := build(sandbox.VerifyPolicy(policy, root))
	if err != nil {
		return nil, fmt.Errorf("verify sandbox unavailable (explicitly disable shell_sandbox or set ZARLCODE_SANDBOX=0 to run unconfined): %w", err)
	}
	return sandbox.NewWorkModeSandbox(normal, verify), nil
}
