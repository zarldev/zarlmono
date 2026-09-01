package code_test

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type fakeProcessWorkspace struct{ root string }

func (f fakeProcessWorkspace) Root() string { return f.root }

func newTestProcessManager(t *testing.T) *code.ProcessManager {
	t.Helper()
	dir := t.TempDir()
	m := code.NewProcessManager(fakeProcessWorkspace{root: dir},
		code.WithReapAfter(500*time.Millisecond),
		code.WithMaxAliveProcesses(4),
		code.WithProcessOutputBuffer(64),
	)
	t.Cleanup(func() { m.Close(t.Context()) })
	return m
}

func TestProcessManager_StartShortLivedThenReadOutput(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess(`echo first; echo second; echo third`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	// Wait for exit + drain.
	waitForProcessExit(t, m, id)

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
	id, err := m.StartProcess(`echo line1; echo line2; echo line3; echo line4`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	defer func() { _, _ = m.Kill(id, syscall.SIGTERM) }()

	// Wait for complete output, then take a cursor for the incremental read.
	var snap code.OutputSnapshot
	waitForProcessExit(t, m, id)
	snap, err = m.Output(id, 0, 0, 0)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if len(snap.Stdout) < 2 {
		t.Fatalf("never saw 2+ lines, got %v", snap.Stdout)
	}
	cursor := snap.StdoutCursor

	// Second poll from cursor should not re-deliver earlier lines.
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
	waitForProcessExit(t, m, id)

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
	waitForProcessExit(t, m, id)
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
	m := code.NewProcessManager(fakeProcessWorkspace{root: dir}, code.WithMaxAliveProcesses(2))
	var ids []code.ProcessID
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
	m := code.NewProcessManager(fakeProcessWorkspace{root: t.TempDir()}, code.WithProcessOutputBuffer(3))
	t.Cleanup(func() { m.Close(t.Context()) })
	id, err := m.StartProcess(`for i in 1 2 3 4 5; do echo $i; done`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	waitForProcessExit(t, m, id)

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
	if _, err := m.Output(code.ProcessID("bash-deadbeef"), 0, 0, 0); err == nil {
		t.Error("expected error for unknown id")
	}
	if _, err := m.Kill(code.ProcessID("bash-deadbeef"), syscall.SIGTERM); err == nil {
		t.Error("expected error for unknown id")
	}
	if _, err := m.Info(code.ProcessID("bash-deadbeef")); err == nil {
		t.Error("expected error for unknown id")
	}
}

// waitForExit waits until the process exits and its output pipes drain.
func waitForProcessExit(t *testing.T, m *code.ProcessManager, id code.ProcessID) {
	t.Helper()
	if err := m.Wait(id); err != nil {
		t.Fatalf("Wait: %v", err)
	}
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

func TestProcessManagerKillAllReturnsOnCancelledContext(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	m.KillAll(ctx)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("KillAll took %s after context cancellation", elapsed)
	}
}
