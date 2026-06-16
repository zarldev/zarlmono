package coderunner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
)

// VerifyOpts tunes CommandGoal. Zero values take the defaults below.
type VerifyOpts struct {
	// PerRunTimeout bounds one verify invocation (the test suite, the build).
	// Zero = defaultVerifyRunTimeout.
	PerRunTimeout time.Duration
	// FeedbackTailBytes caps how much failing output is fed back to the
	// agent. The end of the output is where test failures land. Zero =
	// defaultVerifyTailBytes.
	FeedbackTailBytes int
}

const (
	defaultVerifyRunTimeout = 5 * time.Minute
	defaultVerifyTailBytes  = 4096
)

// CommandGoal makes a shell command the world-checking oracle for verified
// re-drives: pursue.Drive evaluates it after each attempt, a zero exit is
// Done (and marks the Outcome Verified — the verdict came from running the
// command, not from the agent's claim), and a non-zero exit re-drives the
// agent with the command's output tail as feedback.
//
// This is the mechanism that took the SWE-bench eval from a 3/5 single-shot
// ceiling to 8/10 verified: oracle + failure feedback +
// continued context. The eval's oracle is the official grader; here it is
// whatever command the consumer trusts — `go test ./...`, a build, a lint.
//
// The command runs via `sh -c` in root. worktreeState, when non-nil,
// supplies a cheap snapshot of the workspace (e.g. git diff + porcelain
// status): an attempt that changed nothing since the previous verdict
// re-drives immediately with a "you changed nothing" nudge instead of
// re-running the command — that guard caught the empty-patch failure class
// in eval. The state captured at construction is the baseline, so a first
// attempt that does nothing is caught too.
//
// The returned goal is NOT safe for concurrent use; pursue.Drive evaluates
// attempts sequentially, which is the only intended caller.
func CommandGoal(root, command string, worktreeState func() string, opts VerifyOpts) pursue.Goal {
	timeout := opts.PerRunTimeout
	if timeout <= 0 {
		timeout = defaultVerifyRunTimeout
	}
	tailBytes := opts.FeedbackTailBytes
	if tailBytes <= 0 {
		tailBytes = defaultVerifyTailBytes
	}

	var lastState string
	haveState := false
	if worktreeState != nil {
		lastState = worktreeState()
		haveState = true
	}
	lastFeedback := ""

	return pursue.Verified(pursue.GoalFunc(func(ctx context.Context, attempt pursue.Attempt) pursue.Decision {
		if haveState {
			state := worktreeState()
			if state == lastState {
				return pursue.Retry(unchangedFeedback(attempt, lastFeedback))
			}
			lastState = state
		}

		out, err := runVerifyCommand(ctx, root, command, timeout)
		if err == nil {
			return pursue.Done()
		}
		lastFeedback = verifyFeedback(command, attempt, err, outputTailN(out, tailBytes))
		return pursue.Retry(lastFeedback)
	}))
}

// runVerifyCommand runs the oracle once, bounded by timeout, returning its
// combined output and the exit error (nil = verification passed).
func runVerifyCommand(ctx context.Context, root, command string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = root
	// A test runner can fork children that hold the output pipe past the
	// kill; WaitDelay force-closes it so a timed-out verify can't stall the
	// re-drive loop.
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("%w (%w)", ctx.Err(), err)
	}
	return out, err
}

func verifyFeedback(command string, attempt pursue.Attempt, runErr error, tail string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Verification failed after attempt %d: `%s` → %v.\n\n", attempt.Number, command, runErr)
	if tail != "" {
		b.WriteString("Output (tail):\n```\n")
		b.WriteString(tail)
		b.WriteString("\n```\n\n")
	}
	b.WriteString("The workspace still carries your changes. Diagnose the failures above, fix them, and finish when the verification command passes.")
	return b.String()
}

func unchangedFeedback(attempt pursue.Attempt, lastFeedback string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Attempt %d made no changes to the workspace, so verification was not re-run.\n\n", attempt.Number)
	if lastFeedback != "" {
		b.WriteString("The previous verification failure still stands:\n\n")
		b.WriteString(lastFeedback)
	} else {
		b.WriteString("Make concrete edits that address the task, then finish.")
	}
	return b.String()
}

// outputTailN returns the trailing n bytes of out, trimmed — failures
// summarize at the end of test output.
func outputTailN(out []byte, n int) string {
	s := strings.TrimSpace(string(out))
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
