package coderunner

import (
	"context"
	"testing"
	"time"
)

// fakeRun records command invocations and returns scripted results.
type fakeRun struct {
	calls   int
	results []bool // result[i] for the (i+1)th call; last value repeats
}

func (f *fakeRun) run(context.Context, string, []string, time.Duration) bool {
	r := true
	if len(f.results) > 0 {
		i := f.calls
		if i >= len(f.results) {
			i = len(f.results) - 1
		}
		r = f.results[i]
	}
	f.calls++
	return r
}

func TestCommandProbe_NilCmdOrDiffNeverRuns(t *testing.T) {
	t.Parallel()
	fr := &fakeRun{}
	p := newCommandProbe("/root", nil, []string{"go", "test"}, ProbeOpts{}, fr.run, time.Now)
	if p(t.Context()) {
		t.Error("nil diffOf must yield false")
	}
	diff := func() string { return "x" }
	p2 := newCommandProbe("/root", diff, nil, ProbeOpts{}, fr.run, time.Now)
	if p2(t.Context()) {
		t.Error("empty cmd must yield false")
	}
	if fr.calls != 0 {
		t.Errorf("command ran %d times, want 0", fr.calls)
	}
}

func TestCommandProbe_DiffGate(t *testing.T) {
	t.Parallel()
	fr := &fakeRun{results: []bool{false}} // always "not solved"
	diff := "v1"
	p := newCommandProbe("/root", func() string { return diff }, []string{"t"}, ProbeOpts{}, fr.run, time.Now)
	ctx := t.Context()

	p(ctx) // first call always runs
	p(ctx) // diff unchanged → skipped
	p(ctx) // still unchanged → skipped
	if fr.calls != 1 {
		t.Fatalf("unchanged diff ran command %d times, want 1", fr.calls)
	}
	diff = "v2" // agent edited
	p(ctx)
	if fr.calls != 2 {
		t.Fatalf("changed diff should re-run; calls=%d, want 2", fr.calls)
	}
}

func TestCommandProbe_FailClosedThenPass(t *testing.T) {
	t.Parallel()
	fr := &fakeRun{results: []bool{false, true}}
	n := 0
	diff := func() string { n++; return string(rune('a' + n)) } // changes every call
	p := newCommandProbe("/root", diff, []string{"t"}, ProbeOpts{}, fr.run, time.Now)
	ctx := t.Context()
	if p(ctx) {
		t.Error("first run returns false (not solved) → probe false")
	}
	if !p(ctx) {
		t.Error("second run returns true (solved) → probe true")
	}
}

func TestCommandProbe_MaxRuns(t *testing.T) {
	t.Parallel()
	fr := &fakeRun{results: []bool{false}}
	n := 0
	diff := func() string { n++; return string(rune('a' + n)) } // always changing
	p := newCommandProbe("/root", diff, []string{"t"}, ProbeOpts{MaxRuns: 2}, fr.run, time.Now)
	ctx := t.Context()
	for range 5 {
		p(ctx)
	}
	if fr.calls != 2 {
		t.Errorf("MaxRuns=2 but command ran %d times", fr.calls)
	}
}

func TestCommandProbe_MinInterval(t *testing.T) {
	t.Parallel()
	fr := &fakeRun{results: []bool{false}}
	n := 0
	diff := func() string { n++; return string(rune('a' + n)) } // always changing
	clock := time.Unix(0, 0)
	now := func() time.Time { return clock }
	p := newCommandProbe("/root", diff, []string{"t"}, ProbeOpts{MinInterval: 10 * time.Second}, fr.run, now)
	ctx := t.Context()

	p(ctx) // runs at t=0
	clock = clock.Add(3 * time.Second)
	p(ctx) // within MinInterval → skipped
	if fr.calls != 1 {
		t.Fatalf("within MinInterval ran %d times, want 1", fr.calls)
	}
	clock = clock.Add(8 * time.Second) // now t=11s, past the 10s floor
	p(ctx)
	if fr.calls != 2 {
		t.Fatalf("past MinInterval should run; calls=%d, want 2", fr.calls)
	}
}
