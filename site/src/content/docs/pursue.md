---
title: Verified completion
description: The model saying "done" is not evidence. pursue re-drives attempts until a goal you define over real world state actually holds.
---

An agent run ends with a terminal reason — completed, max
iterations, cancelled, error. None of those mean the task is done.
`TerminalCompleted` means precisely "the model stopped calling
tools", which is the model's *opinion*.

`zkit/agent/pursue` separates the two questions. You define a
**Goal** — a predicate over the real world: files on disk, tests
passing, an HTTP endpoint returning 200 — and `pursue.Drive`
re-drives attempts until the goal holds or the budget runs out. Each
re-drive feeds the previous conversation plus your feedback back to
the model, so it sees its own history and the correction.

## The shape

```go
import "github.com/zarldev/zarlmono/zkit/agent/pursue"

// A goal over world state: done when the test file exists AND tests pass.
goal, watcher := pursue.Until(worldIsFixed,
	"tests still failing — read the failure output and try again")

out, err := pursue.Drive(ctx,
	pursue.NewRequest(r.Run,
		runner.TaskSpec{ID: "fix-build", Prompt: prompt},
		pursue.WithGoal(goal),
		pursue.WithWatcher(watcher),
	),
	pursue.WithMaxAttempts(4),
)

switch out.Status() {
case pursue.Statuses.SUCCEEDED: // goal held — verified, not claimed
case pursue.Statuses.GAVEUP:    // attempts exhausted
case pursue.Statuses.ERRORED:   // an attempt failed hard
}
```

`r.Run` satisfies `pursue.AttemptFunc` directly — the harness drives
the same runner you already built.

## Goals

A `Goal` evaluates a finished attempt and returns a `Decision`:
`pursue.Done()` or `pursue.Retry("feedback for the next attempt")`.

For predicate-backed goals there are two constructors:

```go
// static feedback
goal, watcher := pursue.Until(func() bool { return upvoted() },
	"not done yet; if a login screen appeared, log in then retry")

// feedback computed per attempt — name the actual blocking state
goal, watcher := pursue.UntilFunc(func() bool { return upvoted() },
	func(_ context.Context, a pursue.Attempt) string {
		return "still not upvoted (state: " + lastState() + ")"
	},
)
```

For full control implement `pursue.GoalFunc` and inspect the attempt
yourself — that's how an eval harness runs an external verifier and
maps its verdict onto Done/Retry.

Write goals against **the world**, never against `TaskResult`
content. The result's terminal reason tells you why the run stopped;
the goal answers whether the work is done. Those are different
questions — that difference is the whole package.

## Watchers: stop the instant the goal is met

The `Watcher` returned by `Until`/`UntilFunc` polls the predicate
while an attempt is in flight (every 100ms by default) and cancels
the attempt the moment it reports true. The model doesn't get to
keep burning iterations after the work is verifiably done; the run
returns `TerminalCancelled` and the harness reports success — the
goal still gets the final say at the barrier.

For a custom cadence or an expensive probe (run a fast test command,
hit a health endpoint), build the watcher yourself with
`pursue.PollWatcher(probe, interval)`.

## Feedback is the steering wheel

On a not-done verdict, the harness re-drives: the previous attempt's
messages become the next attempt's context, and your feedback string
becomes the prompt. Specific feedback dramatically outperforms
generic — "the top post is not upvoted yet; if a login screen
appeared, call hn_login then hn_upvote_top" gives the model its next
move, "try again" gives it nothing.

`pursue.WithOnAttempt(func(report pursue.AttemptReport) { … })`
surfaces per-attempt progress (number, label, outcome) so a long
re-drive run is legible without reading runner logs.

## Degenerate case: one attempt

`WithMaxAttempts(1)` with no goal is just a supervised single run —
same plumbing, no re-drive. Useful when you want the attempt/report
machinery without an oracle. Headless one-shots are exactly this.

## Why this matters in practice

In SWE-bench-style evaluation, single-shot agent runs plateau well
below what the same model achieves under verified re-drives — most
"failures" are near-misses the model can fix when told *what* is
still broken by something that actually checked. The harness is the
difference between "the model thinks it fixed the bug" and "the
fail-to-pass tests pass."
