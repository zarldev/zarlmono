package runner_test

import (
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// countingSink appends to an unsynchronised slice on every OnContent.
// On its own it races under concurrent calls; wrapped in SyncSink the
// race detector must stay quiet — that's the whole contract.
type countingSink struct {
	runner.NopSink
	got []runner.Content
}

func (c *countingSink) OnContent(e runner.Content) { c.got = append(c.got, e) }

func TestSyncSink_SerialisesConcurrentCalls(t *testing.T) {
	t.Parallel()

	inner := &countingSink{}
	s := runner.NewSyncSink(inner)

	const goroutines, perG = 8, 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perG {
				s.OnContent(runner.Content{})
			}
		}()
	}
	wg.Wait()

	if got, want := len(inner.got), goroutines*perG; got != want {
		t.Errorf("recorded %d events, want %d (lost writes mean the mutex didn't serialise)", got, want)
	}
}

func TestNewSyncSink_PanicsOnNil(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("NewSyncSink(nil) did not panic — the non-nil precondition is documented")
		}
	}()
	runner.NewSyncSink(nil)
}
