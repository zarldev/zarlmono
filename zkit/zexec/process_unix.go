//go:build unix

package zexec

import (
	"os/exec"
	"syscall"
)

// StartProcessGroup configures cmd so the child becomes the leader of a
// new process group. Call before cmd.Start/Run.
func StartProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// KillProcessGroup sends SIGKILL to the process group created by
// StartProcessGroup. If cmd has not started, there is nothing to kill.
func KillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
