package code

import (
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestStartProcess_CapHoldsUnderConcurrentStarts locks the TOCTOU fix:
// the live maxAlive must hold even when many StartProcess calls race through
// the check→fork→insert window at once. Before the pending reservation,
// two callers could both observe alive == maxAlive-1 and both fork, landing
// maxAlive+1 live processes. Every spawned command is a long sleep so none
// exit mid-test to free a slot.
func TestStartProcess_CapHoldsUnderConcurrentStarts(t *testing.T) {
	t.Parallel()
	const maxAlive = 4
	dir := t.TempDir()
	m := NewProcessManager(fakeProcessWorkspace{root: dir}, WithMaxAliveProcesses(maxAlive))

	var (
		mu      sync.Mutex
		ids     []string
		wg      sync.WaitGroup
		started = make(chan struct{})
	)
	for range 64 {
		wg.Go(func() {
			<-started // release everyone at once to maximise the race
			id, err := m.StartProcess(`sleep 30`)
			if err != nil {
				return
			}
			mu.Lock()
			ids = append(ids, id)
			mu.Unlock()
		})
	}
	close(started)
	wg.Wait()

	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = m.Kill(id, syscall.SIGKILL)
		}
	})

	if len(ids) > maxAlive {
		t.Fatalf("started %d processes, maxAlive is %d — TOCTOU window let starts overshoot", len(ids), maxAlive)
	}
	if len(ids) == 0 {
		t.Fatal("no process started at all — maxAlive reservation rejected everything")
	}
	// Independently confirm the manager's own view never exceeds the maxAlive.
	live := 0
	for _, info := range m.List() {
		if info.Running {
			live++
		}
	}
	if live > maxAlive {
		t.Fatalf("manager reports %d live processes, maxAlive is %d", live, maxAlive)
	}
}

// TestSignalGroup_StandsDownWhileReaping locks the pid-reuse guard: once
// a process latches `reaping` (set just before cmd.Wait frees the pid),
// signalGroup must refuse to fire so an escalating SIGKILL can't land on
// a pid-recycled, unrelated process group.
func TestSignalGroup_StandsDownWhileReaping(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess(`sleep 30`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	m.mu.RLock()
	proc := m.procs[id]
	m.mu.RUnlock()
	if proc == nil {
		t.Fatal("process not tracked after start")
	}
	// Kill the group directly on cleanup: once we force `reaping` below,
	// m.Kill would (correctly) stand down and leave the sleep running.
	t.Cleanup(func() { _ = syscall.Kill(-proc.pid, syscall.SIGKILL) })

	// Live process: signal 0 is a no-op liveness probe — it fires and
	// reports true.
	if !proc.signalGroup(syscall.Signal(0)) {
		t.Fatal("signalGroup returned false for a live, un-reaped process")
	}

	// Simulate the reap latch and assert signalGroup now stands down.
	proc.lifecycleMu.Lock()
	proc.reaping = true
	proc.lifecycleMu.Unlock()
	if proc.signalGroup(syscall.SIGKILL) {
		t.Fatal("signalGroup fired while reaping — pid-recycle window is open")
	}

	// Same contract once fully exited.
	proc.lifecycleMu.Lock()
	proc.exited = true
	proc.lifecycleMu.Unlock()
	if proc.signalGroup(syscall.SIGTERM) {
		t.Fatal("signalGroup fired after exit")
	}
}

// TestDrainPipe_TruncatesLongLineAndKeepsDraining locks the silent-abort
// fix: a line over the maxAlive is truncated (with a marker) but the stream
// keeps draining, so output AFTER the giant line still reaches the ring.
// The old bufio.Scanner returned ErrTooLong, ended the loop, and closed
// the read end of a pipe the process was still writing to.
//
// Not parallel: it shrinks the package-level maxAlive for the duration.
func TestDrainPipe_TruncatesLongLineAndKeepsDraining(t *testing.T) {
	orig := maxDrainLineBytes
	maxDrainLineBytes = 32
	defer func() { maxDrainLineBytes = orig }()

	pr, pw := io.Pipe()
	ring := newLineRingBuffer(16)
	var wg sync.WaitGroup
	wg.Add(1)
	go drainPipe(pr, ring, &wg)

	long := strings.Repeat("x", 200) // well over the 32-byte maxAlive
	go func() {
		_, _ = io.WriteString(pw, long+"\n")
		_, _ = io.WriteString(pw, "after\n")
		_ = pw.Close()
	}()

	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("drainPipe never returned — it wedged on the long line")
	}

	lines, _, _ := ring.ReadSince(0, 0)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (truncated long line + \"after\"): %q", len(lines), bytesToStrings(lines))
	}
	got0 := string(lines[0])
	if !strings.HasPrefix(got0, strings.Repeat("x", 32)) {
		t.Errorf("line 0 = %q, want the first 32 x's retained", got0)
	}
	if !strings.Contains(got0, "truncated") {
		t.Errorf("line 0 = %q, want a truncation marker", got0)
	}
	if string(lines[1]) != "after" {
		t.Errorf("line 1 = %q, want \"after\" — the stream aborted after the long line", lines[1])
	}
}
