// Package pursue drives an agent run toward a programmatic goal. It
// re-drives the conversation with corrective feedback until a Goal
// reports the task complete or an attempt budget is exhausted.
//
// The re-drive control flow is deterministic given a fixed Goal and a
// fixed sequence of AttemptFunc returns; the model itself remains
// stochastic. The pursue loop bounds and structures a non-deterministic
// process — it does not make the process deterministic.
//
// This package depends on zkit/agent/runner — it is not a general
// "drive any function until a predicate" library. The dependency points
// one way: pursue → runner. The runner never depends on pursue.
//
// "headless" is the degenerate configuration: one attempt with the
// AcceptCompleted oracle (run once, trust the terminal reason, no
// re-drive). Verified-completion modes raise MaxAttempts and supply a
// Goal that inspects the world rather than trusting the model's "done".
package pursue
