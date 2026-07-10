package engine

import (
	"errors"
	"strings"
	"syscall"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// KillProcess terminates a background process managed by the live runner and
// returns a fresh process snapshot plus the manager-reported exit code.
func (l *LiveRunner) KillProcess(processID, signal string) (code.ProcessInfo, int, error) {
	if l == nil {
		return code.ProcessInfo{}, 0, errors.New("live runner unavailable")
	}
	l.mu.Lock()
	pm := l.pm
	l.mu.Unlock()
	if pm == nil {
		return code.ProcessInfo{}, 0, errors.New("process manager unavailable")
	}
	exitCode, err := pm.Kill(code.ProcessID(processID), signalForProcessKill(signal))
	info, infoErr := pm.Info(code.ProcessID(processID))
	if err != nil {
		return info, exitCode, err
	}
	if infoErr != nil {
		return info, exitCode, infoErr
	}
	return info, exitCode, nil
}

// Canonical kill-signal tokens shared by NormalizeKillSignal and
// signalForProcessKill (and the TUI's process-kill handlers).
const (
	signalKill = "KILL"
	signalINT  = "INT"
	signalTERM = "TERM"
)

// NormalizeKillSignal maps a user-supplied signal name to a canonical
// KILL/INT/TERM token. It is pure (no bubbletea, no *UI) so it lives with the
// engine; the TUI's process-kill handlers call it too.
func NormalizeKillSignal(signal string) string {
	switch strings.ToUpper(strings.TrimSpace(signal)) {
	case signalKill, "SIGKILL":
		return signalKill
	case signalINT, "SIGINT":
		return signalINT
	default:
		return signalTERM
	}
}

// signalForProcessKill resolves a signal name to its syscall.Signal. Pure helper
// used by KillProcess.
func signalForProcessKill(signal string) syscall.Signal {
	switch NormalizeKillSignal(signal) {
	case signalKill:
		return syscall.SIGKILL
	case signalINT:
		return syscall.SIGINT
	default:
		return syscall.SIGTERM
	}
}
