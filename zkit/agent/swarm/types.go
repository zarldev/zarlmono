// Package swarm owns goal-scoped agent sessions across runner turns. Findings
// are retained independently of notifications; messages activate idle members.
// It is in-process only and provides no restart durability or remote delivery.
package swarm

import (
	"context"
	"errors"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// AgentID is a stable logical identity, distinct from each execution's task ID.
type AgentID string

var (
	// ErrClosed reports a stopped goal or member that cannot accept new work.
	ErrClosed = errors.New("swarm closed")
	// ErrStarted reports a second attempt to run the same goal owner.
	ErrStarted = errors.New("swarm already started")
	// ErrConflict reports duplicate agent membership.
	ErrConflict = errors.New("swarm agent already exists")
	// ErrNotFound reports an unknown logical agent.
	ErrNotFound = errors.New("swarm agent not found")
	// ErrCapacity reports exhausted membership capacity.
	ErrCapacity = errors.New("swarm membership capacity exhausted")
	// ErrBudget reports exhausted goal activation, time, or reported-token budget.
	ErrBudget = errors.New("swarm budget exhausted")
)

// Limits is trusted host policy. Supply positive members, workers, activations,
// and runtime. ReportedTokens is optional (zero disables it). By default it is
// an admission-time ceiling over direct activation-result totals. With
// WithInferenceAccounting, it instead becomes a settlement threshold over all
// explicitly scoped provider calls, including auxiliary and descendant
// consumption. Providers may omit usage, and in-flight work can overshoot either
// threshold.
type Limits struct {
	Members        int
	Workers        int
	Activations    int
	Runtime        time.Duration
	ReportedTokens int
	// Invocations limits provider-stream calls when WithInferenceAccounting is enabled.
	// Zero disables it. Token accounting then includes every explicitly scoped call.
	Invocations int
	Board       board.Limits
}

// DefaultLimits bounds a local experiment to 16 members, four simultaneous turns,
// 128 activations, 30 minutes, and 500,000 reported tokens.
func DefaultLimits() Limits {
	return Limits{Members: 16, Workers: 4, Activations: 128, Runtime: 30 * time.Minute,
		ReportedTokens: 500000, Board: board.DefaultLimits()}
}

// WithLimits assigns already validated host policy without starting work.
func WithLimits(limits Limits) options.Option[Swarm] {
	return func(s *Swarm) { s.limits = limits }
}

// Execute runs and settles one activation synchronously. It must not return until
// tools, children, history/context commit, and any host persistence have settled.
// The owner never invokes it concurrently for the same member. An error suspends
// that member rather than starting an unbounded automatic retry loop.
type Execute func(context.Context, Activation) runner.TaskResult

// Agent supplies a stable identity, original assignment, and host execution
// capability. Task's context/attachments are cloned when membership is admitted.
// The host retains permission policy in Execute; messages cannot change it.
type Agent struct {
	ID      AgentID
	Task    runner.TaskSpec
	Execute Execute
}

// Activation is a single execution with a fresh task ID and owned continuation.
// Spec's initial human assignment is sent only on the first execution. Later
// turns receive new board observations through Inputs, not forged human prompts.
type Activation struct {
	Spec   runner.TaskSpec
	Member Member
	runID  taskscope.ID
}

// Snapshot is an independent logical-agent view, without lifecycle handles.
type Snapshot struct {
	ID          AgentID
	State       State
	RunID       taskscope.ID
	Activations int
	Pending     int
	Messages    []llm.Message
	LastError   error
}
