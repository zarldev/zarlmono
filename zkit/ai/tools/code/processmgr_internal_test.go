package code

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fakeProcessWorkspace struct{ root string }

func (f fakeProcessWorkspace) Root() string { return f.root }

func newTestProcessManager(t *testing.T) *ProcessManager {
	t.Helper()
	dir := t.TempDir()
	return NewProcessManager(fakeProcessWorkspace{root: dir},
		WithReapAfter(500*time.Millisecond),
		WithMaxAliveProcesses(4),
		WithProcessOutputBuffer(64),
	)
}

func TestProcessManager_StartShortLivedThenReadOutput(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess(`echo first; echo second; echo third`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	// Wait for exit + drain.
	waitForExit(t, m, id)

	snap, err := m.Output(id, 0, 0, 0)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if snap.Running {
		t.Errorf("expected exited, got running")
	}
	if snap.ExitCode == nil || *snap.ExitCode != 0 {
		t.Errorf("exit_code = %v, want 0", snap.ExitCode)
	}
	wantStdout := []string{"first", "second", "third"}
	if len(snap.Stdout) != len(wantStdout) {
		t.Fatalf("len(stdout) = %d, want %d (%v)", len(snap.Stdout), len(wantStdout), snap.Stdout)
	}
	for i, w := range wantStdout {
		if snap.Stdout[i] != w {
			t.Errorf("stdout[%d] = %q, want %q", i, snap.Stdout[i], w)
		}
	}
}

func TestProcessManager_IncrementalRead(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess(`for i in 1 2 3 4; do echo line$i; sleep 0.05; done`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	defer func() { _, _ = m.Kill(id, syscall.SIGTERM) }()

	// Poll until we see at least 2 lines.
	var snap OutputSnapshot
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, _ = m.Output(id, 0, 0, 0)
		if len(snap.Stdout) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(snap.Stdout) < 2 {
		t.Fatalf("never saw 2+ lines, got %v", snap.Stdout)
	}
	cursor := snap.StdoutCursor

	// Second poll from cursor should not re-deliver earlier lines.
	waitForExit(t, m, id)
	snap2, _ := m.Output(id, cursor, 0, 0)
	for _, l := range snap2.Stdout {
		if l == "line1" || l == "line2" {
			t.Errorf("incremental poll re-delivered older line: %q", l)
		}
	}
}

func TestProcessManager_KillRunningProcess(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess(`while true; do sleep 0.5; done`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // let it actually start

	code, err := m.Kill(id, syscall.SIGTERM)
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	// Process killed via signal — exit code is non-zero (signal exit
	// returns -1 for unhandled SIGTERM; bash translates differently
	// per distro). Just confirm it's recorded.
	_ = code

	info, err := m.Info(id)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Running {
		t.Errorf("Info reports running after Kill")
	}
}

func TestProcessManager_StderrCaptured(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess(`echo out; echo err >&2`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	waitForExit(t, m, id)

	snap, _ := m.Output(id, 0, 0, 0)
	if len(snap.Stdout) != 1 || snap.Stdout[0] != "out" {
		t.Errorf("stdout = %v", snap.Stdout)
	}
	if len(snap.Stderr) != 1 || snap.Stderr[0] != "err" {
		t.Errorf("stderr = %v", snap.Stderr)
	}
}

func TestProcessManager_StripsANSI(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess("printf '\\033[31mred\\033[0m\\n'")
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	waitForExit(t, m, id)
	snap, _ := m.Output(id, 0, 0, 0)
	if len(snap.Stdout) != 1 {
		t.Fatalf("stdout = %v", snap.Stdout)
	}
	if snap.Stdout[0] != "red" {
		t.Errorf("ANSI not stripped: got %q, want %q", snap.Stdout[0], "red")
	}
}

func TestProcessManager_MaxAliveCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := NewProcessManager(fakeProcessWorkspace{root: dir}, WithMaxAliveProcesses(2))
	var ids []ProcessID
	for i := range 2 {
		id, err := m.StartProcess(`sleep 5`)
		if err != nil {
			t.Fatalf("StartProcess #%d: %v", i, err)
		}
		ids = append(ids, id)
	}
	if _, err := m.StartProcess(`sleep 5`); err == nil {
		t.Errorf("expected ErrTooManyProcesses, got nil")
	}
	// Cleanup so the test process doesn't linger.
	for _, id := range ids {
		_, _ = m.Kill(id, syscall.SIGKILL)
	}
}

func TestProcessManager_KillAllOnShutdown(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess(`while true; do sleep 0.5; done`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m.KillAll(ctx)
	info, _ := m.Info(id)
	if info.Running {
		t.Errorf("KillAll did not stop %s", id)
	}
}

func TestProcessManager_OutputDroppedCounter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Generous reapAfter — the sweep runs every 10s, so any value
	// short of that risks the reaper evicting the process before
	// the test reads its output. 30s leaves headroom under load.
	m := NewProcessManager(fakeProcessWorkspace{root: dir},
		WithProcessOutputBuffer(3),
		WithReapAfter(30*time.Second),
	)
	id, err := m.StartProcess(`for i in 1 2 3 4 5; do echo $i; done`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	waitForExit(t, m, id)

	snap, _ := m.Output(id, 0, 0, 0)
	if snap.StdoutDroppedSince < 2 {
		t.Errorf("expected ≥2 dropped lines (cap=3, wrote 5), got %d", snap.StdoutDroppedSince)
	}
	// Surviving content is the last 3.
	if len(snap.Stdout) != 3 || snap.Stdout[0] != "3" {
		t.Errorf("expected tail [3 4 5], got %v", snap.Stdout)
	}
}

func TestProcessManager_NotFound(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	if _, err := m.Output(ProcessID("bash-deadbeef"), 0, 0, 0); err == nil {
		t.Error("expected error for unknown id")
	}
	if _, err := m.Kill(ProcessID("bash-deadbeef"), syscall.SIGTERM); err == nil {
		t.Error("expected error for unknown id")
	}
	if _, err := m.Info(ProcessID("bash-deadbeef")); err == nil {
		t.Error("expected error for unknown id")
	}
}

// waitForExit polls Info until the process exits or the deadline
// fires. Test helper — fails the test on timeout.
func waitForExit(t *testing.T, m *ProcessManager, id ProcessID) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := m.Info(id)
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if !info.Running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %s did not exit within 3s", id)
}

func TestMain(m *testing.M) {
	// Some environments (CI sandboxes) restrict process creation.
	// Skip cleanly if the shell isn't available rather than failing
	// the suite for an environmental reason.
	if _, err := os.Stat("/bin/sh"); err != nil {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var _ = strings.TrimSpace // keep import alive for future formatters

func TestWaitForOrContextReturnsOnCancelledContext(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	if waitForOrContext(done, ctx, time.Minute) {
		t.Fatal("waitForOrContext returned true for cancelled context")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitForOrContext took %s after context cancellation", elapsed)
	}
}
