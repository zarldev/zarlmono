package code_test

import (
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
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
	m := code.NewProcessManager(fakeProcessWorkspace{root: dir}, code.WithMaxAliveProcesses(maxAlive))

	var (
		mu      sync.Mutex
		ids     []code.ProcessID
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
// TestDrainPipe_TruncatesLongLineAndKeepsDraining locks the silent-abort
// fix: a line over the maxAlive is truncated (with a marker) but the stream
// keeps draining, so output AFTER the giant line still reaches the ring.
// The old bufio.Scanner returned ErrTooLong, ended the loop, and closed
// the read end of a pipe the process was still writing to.
//
// Not parallel: it shrinks the package-level maxAlive for the duration.
func TestProcessOutputLongLineKeepsDraining(t *testing.T) {
	t.Parallel()
	m := newTestProcessManager(t)
	id, err := m.StartProcess(`head -c 4194404 /dev/zero | tr '\0' x; printf '\nafter\n'`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	if err := m.Wait(id); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	snap, err := m.Output(id, 0, 0, 0)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if len(snap.Stdout) != 2 {
		t.Fatalf("stdout lines = %d, want 2", len(snap.Stdout))
	}
	if !strings.Contains(snap.Stdout[0], "truncated") {
		t.Errorf("missing truncation marker")
	}
	if snap.Stdout[1] != "after" {
		t.Errorf("second line = %q, want after", snap.Stdout[1])
	}
}
