// Package sleepinhibit prevents the operating system from suspending while work is active.
package sleepinhibit

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
)

// Inhibitor holds an operating-system sleep inhibition until Close is called.
type Inhibitor struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

// Acquire starts an operating-system sleep inhibitor for an active agent turn.
// Linux uses systemd's sleep inhibitor and macOS uses caffeinate. Other systems
// return an error rather than pretending that suspension has been inhibited.
func Acquire(ctx context.Context) (*Inhibitor, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.CommandContext(ctx, "systemd-inhibit",
			"--what=sleep",
			"--who=zarlcode",
			"--why=Agent work is in progress",
			"--mode=block",
			"sleep", "infinity",
		)
	case "darwin":
		cmd = exec.CommandContext(ctx, "caffeinate", "-i")
	default:
		return nil, fmt.Errorf("sleep inhibition is not supported on %s", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start sleep inhibitor: %w", err)
	}

	i := &Inhibitor{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(i.done)
	}()
	return i, nil
}

// Close releases the sleep inhibition and waits for its helper process to exit.
func (i *Inhibitor) Close() error {
	if i == nil {
		return nil
	}
	var killErr error
	i.once.Do(func() {
		select {
		case <-i.done:
			return
		default:
		}
		killErr = i.cmd.Process.Kill()
		<-i.done
	})
	return killErr
}
