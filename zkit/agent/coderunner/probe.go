package coderunner

import (
	"context"
	"hash/fnv"
	"os/exec"
	"time"
)

// ProbeOpts bounds a CommandProbe so a periodic "is it solved yet?" check
// can't become a cost sink. Each zero-valued field disables its guard.
type ProbeOpts struct {
	// PerRunTimeout caps a single command invocation; the command's ctx is
	// derived from the probe ctx with this deadline. Zero = no per-run bound
	// (the probe ctx still applies).
	PerRunTimeout time.Duration
	// MaxRuns caps how many times the command runs over the probe's lifetime
	// (one attempt). Zero = unlimited.
	MaxRuns int
	// MinInterval is the floor between two command runs even when the diff
	// keeps changing. Zero = no floor (the probe is still diff-gated).
	MinInterval time.Duration
}

// CommandProbe builds a probe for [harness.PollWatcher] that answers "is the
// task already solved?" by running cmd in root and checking for a zero exit.
// It is a STOP-HINT, not an oracle — the authoritative Goal still decides at
// the barrier, so a false positive costs one early evaluation, never a wrong
// verdict.
//
// To keep most ticks near-free, cmd runs only when the tracked diff (diffOf)
// changed since the last run — the agent can only have "just solved" it right
// after an edit, so an unchanged diff means there's nothing new to check. A
// cmd that errors, times out, or can't run yields false: fail-closed, an
// inconclusive probe never stops the agent.
//
// The returned probe is NOT safe for concurrent use; PollWatcher calls it from
// a single goroutine, which is the only intended caller.
func CommandProbe(root string, diffOf func() string, cmd []string, opts ProbeOpts) func(context.Context) bool {
	return newCommandProbe(root, diffOf, cmd, opts, execRun, time.Now)
}

// execRun runs cmd in dir and reports a zero exit, bounded by timeout.
func execRun(ctx context.Context, dir string, cmd []string, timeout time.Duration) bool {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Dir = dir
	return c.Run() == nil
}

// newCommandProbe is CommandProbe with the command runner and clock injected,
// so the diff-gating / bounds logic is testable without shelling out or
// sleeping.
func newCommandProbe(
	root string,
	diffOf func() string,
	cmd []string,
	opts ProbeOpts,
	run func(ctx context.Context, dir string, cmd []string, timeout time.Duration) bool,
	now func() time.Time,
) func(context.Context) bool {
	var (
		ran      bool
		lastHash uint64
		runs     int
		lastRun  time.Time
	)
	return func(ctx context.Context) bool {
		if len(cmd) == 0 || diffOf == nil {
			return false
		}
		h := hashString(diffOf())
		// Diff-gate: skip when nothing changed since the last actual run.
		// lastHash is only advanced on a real run, so a change that's
		// blocked below (by MaxRuns/MinInterval) stays "pending" and runs
		// once the guard clears.
		if ran && h == lastHash {
			return false
		}
		if opts.MaxRuns > 0 && runs >= opts.MaxRuns {
			return false
		}
		if opts.MinInterval > 0 && !lastRun.IsZero() && now().Sub(lastRun) < opts.MinInterval {
			return false
		}
		ran = true
		lastHash = h
		lastRun = now()
		runs++
		return run(ctx, root, cmd, opts.PerRunTimeout)
	}
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
