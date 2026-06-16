package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const systemPrompt = `You upvote the top post on Hacker News.

Tools:
  hn_upvote_top — upvote the current top post.
  hn_login      — sign in to the configured account.

If upvoting reports you are not logged in, call hn_login and then upvote
again. You are done only once the upvote is confirmed.`

const goalPrompt = "Upvote the top post on Hacker News."

// RunUpvote drives client toward a verified upvote of the HN top post.
//
// It assembles the two tools, wraps them with the require_auth rail
// (hn_upvote_top is gated on the session being logged in), and hands the
// runner to the harness with a WORLD-verifying oracle: the goal is met
// only when the session confirms the upvote registered — not when the
// model claims it did. The harness re-drives with corrective feedback up
// to maxAttempts.
//
// The runner.Client is injected so the same wiring serves a real LLM
// (main) and a scripted client (the deterministic test) unchanged.
func RunUpvote(ctx context.Context, client runner.Client, sess *Session, maxAttempts int) pursue.Outcome {
	reg := tools.NewRegistry(&upvoteTop{s: sess}, &login{s: sess})

	// The rail: upvoting is blocked until authenticated. Login detection
	// lives here, not in the tool — the actuator stays dumb.
	source := guardrails.NewGuardedSource(reg,
		NewRequireAuth(sess.LoggedIn,
			"you are not logged in — call hn_login first, then retry", ToolUpvoteTop))

	r := runner.New(client, runner.WithTools(source),
		runner.WithPromptText(systemPrompt),
		// Backstop: a stuck browser action can't burn the default 5-minute
		// tool timeout. The chromedp adapter bounds each action tighter
		// (actionTimeout) and reports a labeled error, so this rarely fires.
		runner.WithToolTimeout(45*time.Second),
	)

	// The oracle: verified world state, not the model's say-so.
	goal, watcher := pursue.UntilFunc(sess.VerifiedUpvoted, func(_ context.Context, _ pursue.Attempt) string {
		return "The top post is not upvoted yet (state: " + sess.LastState() +
			"). If a login screen appeared, call hn_login, then hn_upvote_top."
	})

	return pursue.Drive(ctx, pursue.NewRequest(r.Run, runner.TaskSpec{ID: "hn-upvote", Prompt: goalPrompt}, pursue.WithGoal(goal), pursue.WithWatcher(watcher)),
		pursue.WithMaxAttempts(maxAttempts),
		// Surface progress per re-drive so the run is legible without
		// reading the runner's iteration logs.
		pursue.WithOnAttempt(func(report pursue.AttemptReport) {
			fmt.Fprintf(os.Stderr, "attempt %d/%d: %s (state=%q)\n",
				report.Attempt.Number, maxAttempts, pursue.LabelAttempt(report), sess.LastState())
		}),
	)
}
