//go:build unix

package zexec_test

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/zexec"
)

func TestProcessGroupKillTerminatesStartedCommand(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	zexec.StartProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })

	if err := zexec.KillProcessGroup(cmd); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("KillProcessGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-time.After(3 * time.Second):
		t.Fatal("command did not exit after process-group kill")
	case <-done:
	}
}
