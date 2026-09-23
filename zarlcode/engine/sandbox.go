package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
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
		return nil, fmt.Errorf("shell sandbox unavailable (explicitly turn off the Shell > sandbox setting or set ZARLCODE_SANDBOX=0 to run unconfined): %w", err)
	}
	verify, err := build(sandbox.VerifyPolicy(policy, root))
	if err != nil {
		return nil, fmt.Errorf("verify sandbox unavailable (explicitly turn off the Shell > sandbox setting or set ZARLCODE_SANDBOX=0 to run unconfined): %w", err)
	}
	return sandbox.NewWorkModeSandbox(normal, verify), nil
}

// ResolveShellSandbox resolves startup confinement with environment overrides
// taking precedence over effective preferences. Only an unset Darwin preference
// requests consent to run unconfined; acceptance records sandbox=off for this
// workspace. Declining returns context.Canceled without changing preferences.
// Explicit enables and all other platform defaults still require confinement.
// confirm must not prompt in headless launches; it should return an actionable error.
func (s *Settings) ResolveShellSandbox(ctx context.Context, platform string, confirm func(context.Context) (bool, error)) (bool, error) {
	if enabled, ok := sandbox.EnvOverride(); ok {
		return enabled, nil
	}
	value, err := s.Svc.GetSetting(ctx, prefs.ScopeEffective, prefs.KeySandbox)
	if err == nil {
		switch value.Value {
		case "on":
			return true, nil
		case "off":
			return false, nil
		default:
			return false, fmt.Errorf("invalid sandbox setting %q: expected on or off", value.Value)
		}
	}
	if !errors.Is(err, prefs.ErrNotFound) {
		return false, fmt.Errorf("read sandbox preference: %w", err)
	}
	if platform != "darwin" {
		return true, nil
	}
	accepted, err := confirm(ctx)
	if err != nil {
		return false, fmt.Errorf("confirm unconfined shell: %w", err)
	}
	if !accepted {
		return false, context.Canceled
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeySandbox, "off"); err != nil {
		return false, fmt.Errorf("save unconfined shell consent: %w", err)
	}
	return false, nil
}
