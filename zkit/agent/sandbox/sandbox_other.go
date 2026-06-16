//go:build !linux

package sandbox

import (
	"errors"
	"os/exec"
)

// Sandbox is unavailable off Linux — Landlock and network namespaces
// are Linux kernel features. The type exists so consumers cross-compile;
// New always errors and callers fall back to running unsandboxed.
type Sandbox struct {
	policy Policy
}

// New reports that confinement is unsupported on this platform.
func New(Policy) (*Sandbox, error) {
	return nil, errors.New("sandbox: unsupported platform (requires linux)")
}

// Policy returns the grant set this sandbox was built with.
func (s *Sandbox) Policy() Policy { return s.policy }

// Sandbox errors unconditionally — New never hands out a usable
// Sandbox on this platform.
func (s *Sandbox) Sandbox(*exec.Cmd) error {
	return errors.New("sandbox: unsupported platform (requires linux)")
}

// ExecShim is a no-op off Linux: no Sandbox can have spawned a shim
// child here.
func ExecShim() {}

// ABIVersion reports 0 — no Landlock outside Linux.
func ABIVersion() int { return 0 }
