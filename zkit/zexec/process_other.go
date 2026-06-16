//go:build !unix

package zexec

import "os/exec"

// StartProcessGroup is a no-op on platforms where process-group setup is
// not implemented by this package. Callers can still use KillProcessGroup
// to terminate the direct child process.
func StartProcessGroup(cmd *exec.Cmd) {}

// KillProcessGroup terminates the direct child process on platforms where
// process-group signalling is not available.
func KillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
