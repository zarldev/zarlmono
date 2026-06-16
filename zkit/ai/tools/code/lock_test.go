package code_test

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestWorkspaceLockPathSerialisesConcurrentHolders(t *testing.T) {
	t.Parallel()

	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	const n = 8
	var (
		active int32
		peak   int32
		wg     sync.WaitGroup
	)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			unlock := ws.LockPath("/some/canonical/path")
			defer unlock()
			cur := atomic.AddInt32(&active, 1)
			defer atomic.AddInt32(&active, -1)
			for {
				old := atomic.LoadInt32(&peak)
				if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got != 1 {
		t.Errorf("peak concurrent holders=%d, want 1 (LockPath should serialise same-key callers)", got)
	}
}

func TestWorkspaceLockPathDoesNotBlockDifferentPaths(t *testing.T) {
	t.Parallel()

	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	a := ws.LockPath("/path/a")
	defer a()

	done := make(chan struct{})
	go func() {
		b := ws.LockPath("/path/b")
		b()
		close(done)
	}()

	select {
	case <-done:
		// expected: different keys take independent locks
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("LockPath blocked across distinct keys")
	}
}

func TestWriteEditConcurrentSerialisedByPathLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ws, err := code.NewWorkspace(dir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	w := code.NewWriteTool(ws)
	e := code.NewEditTool(ws)

	// Seed a file the edit can then mutate.
	target := "shared.txt"
	abs := filepath.Join(dir, target)
	_ = abs // future asserts may want it

	seed := func(body string) {
		if r := execTyped(t, w, code.WriteArgs{Path: target, Content: body}); !r.Success {
			t.Fatalf("seed write: %s", r.Error)
		}
	}
	seed("AAAA")

	// Two concurrent edits that target the same string. Without the
	// per-path lock both edits read the same body, both compute their
	// replacement, and the second write wins — losing one of them.
	// With the lock the second edit observes the first's write and
	// either succeeds against the new state or fails to find the
	// old_string. Either outcome is fine; the invariant we want is
	// that the final body is consistent with the operations actually
	// performed (no torn / interleaved write).
	const n = 4
	var (
		successes int32
		wg        sync.WaitGroup
	)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			res := execTyped(t, e, code.EditArgs{Path: target, OldString: "A", NewString: "B"})
			if res != nil && res.Success {
				atomic.AddInt32(&successes, 1)
			}
			_ = i
		}()
	}
	wg.Wait()

	// The contract: if any edit succeeded, the file body must reflect
	// exactly that many A->B replacements. Without the lock, two
	// edits could both succeed-from-cache but only one's write
	// reaches disk, so the byte-count of B's would mismatch the
	// successes counter.
	final, err := readWorkspaceFile(dir, target)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	bs := 0
	for _, c := range final {
		if c == 'B' {
			bs++
		}
	}
	if int32(bs) != successes {
		t.Errorf("final B-count=%d, successful edits=%d (lost-update under concurrency)", bs, successes)
	}
}

func readWorkspaceFile(root, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
