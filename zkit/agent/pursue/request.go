package pursue

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// RequestOption configures a Request. See WithGoal and WithWatcher.
type RequestOption func(*requestConfig)

type requestConfig struct {
	goal    Goal
	watcher Watcher
}

// WithGoal sets the Goal for a Request. When omitted, Goal defaults to nil
// (AcceptCompleted — the headless/trust-the-model shape).
func WithGoal(goal Goal) RequestOption {
	return func(c *requestConfig) { c.goal = goal }
}

// WithWatcher sets the early-stop Watcher for a Request. When omitted,
// Drive runs each attempt to natural completion before consulting the
// Goal — there is no in-flight cancellation.
func WithWatcher(w Watcher) RequestOption {
	return func(c *requestConfig) { c.watcher = w }
}

// Request is the contract for a single Drive call. Goal may be nil, in
// which case AcceptCompleted is used (the headless default). Watcher may
// be nil to disable early stop.
type Request struct {
	Attempt AttemptFunc
	Spec    runner.TaskSpec
	Goal    Goal
	Watcher Watcher
}

// NewRequest returns a Request ready for Drive. Attempt and Spec are
// required; WithGoal and WithWatcher are the options.
func NewRequest(attempt AttemptFunc, spec runner.TaskSpec, opts ...RequestOption) Request {
	var cfg requestConfig
	for _, o := range opts {
		o(&cfg)
	}
	return Request{Attempt: attempt, Spec: spec, Goal: cfg.goal, Watcher: cfg.watcher}
}
