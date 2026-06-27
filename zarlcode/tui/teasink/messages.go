package teasink

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Each message type mirrors the corresponding [runner.X] event
// payload, with TaskID converted to string for ergonomics. The
// Sink methods construct these directly.
//
// Copied verbatim from pkg/agent/tui/teasink (the v1 bridge): these
// structs are framework-agnostic (no bubbletea import), so the runner
// event vocabulary ports unchanged to v2. Only the Sink delivery
// (program.Send) is v2-specific and lives in the sibling sink file.

// ContentMsg fires for each streamed assistant-message delta.
type ContentMsg struct {
	TaskID string
	Depth  int
	Delta  string
}

// ThinkingMsg fires for each streamed reasoning delta (extended thinking /
// chain-of-thought), carried separately from ContentMsg so the TUI renders
// it in the thinking surface rather than the response body.
type ThinkingMsg struct {
	TaskID string
	Depth  int
	Delta  string
}

// ToolStartedMsg fires when the runner dispatches a tool call.
type ToolStartedMsg struct {
	TaskID     string
	Depth      int
	ToolID     string
	ToolName   string
	Parameters map[string]any
}

// ToolCompletedMsg fires when a tool call returns successfully.
type ToolCompletedMsg struct {
	TaskID          string
	Depth           int
	ToolID          string
	ToolName        string
	Result          any
	FormattedResult string
	Effects         []tools.Effect
	Duration        time.Duration
}

// ToolFailedMsg fires when a tool call errors or reports failure.
type ToolFailedMsg struct {
	TaskID    string
	Depth     int
	ToolID    string
	ToolName  string
	Error     string
	Kind      tools.Kind // typed failure classification (validation / transient / …)
	Abandoned bool       // timed out with its goroutine possibly still in flight
	Effects   []tools.Effect
	Duration  time.Duration
}

// ConversationStartedMsg marks the start of a Run. Prompt carries
// the seed prompt for the (sub-)agent so the stack pane can label
// the frame with what's running rather than its opaque TaskID.
//
// ParentToolCallID identifies the tool call that spawned this Run
// (set by spawn-agent on sub-agent dispatch). Empty for top-level
// Runs. Lets the model attribute Depth>0 events back to the exact
// parent tool call — necessary when multiple spawn_agent calls run
// in parallel and ToolID-based correlation is the only unambiguous
// link from child events to a parent slot.
type ConversationStartedMsg struct {
	TaskID           string
	Depth            int
	Prompt           string
	ParentToolCallID string
	AgentName        string
}

// ConversationEndedMsg marks a Run reaching its single terminal state.
// Reason classifies the outcome (completed, max_iterations, cancelled, or
// error); Error carries the message when Reason is error. The consumer
// switches on Reason — a finished answer, a truncated max-iterations turn,
// a cancellation, and a fault all arrive here. TotalUsage / Iterations /
// ParentToolCallID let the consumer attribute the full token spend of the
// Run back to its initiator — especially useful for sub-agent Runs
// (Depth > 0) whose token cost is otherwise invisible at the parent's
// session counters.
type ConversationEndedMsg struct {
	TaskID string
	Depth  int
	Reason runner.TerminalReason
	Error  string
	// RateLimit carries structured rate-limit timing when the terminal
	// error was a rate limit; nil otherwise. The consumer renders from
	// these fields rather than re-parsing Error.
	RateLimit        *llm.RateLimitError
	Duration         time.Duration
	Iterations       int
	TotalUsage       *llm.Usage
	ParentToolCallID string
}

// IterationCompletedMsg fires at the end of each iteration within a
// Run. Usage is the Run's current occupancy — the most recent observed
// [llm.Usage], which never regresses to nil mid-Run — and keeps the
// mid-turn token gauges fresh; without it the gauge stays on a
// chars-based estimate until the Run completes. Delta is this
// iteration's own usage, nil when the provider dropped it (llama.cpp's
// openai-compat endpoint is known to on the terminal chunk); consumers
// sum Delta for per-iteration flow figures like the live tok/s estimate.
type IterationCompletedMsg struct {
	TaskID string
	Depth  int
	Iter   int
	Usage  *llm.Usage
	Delta  *llm.Usage
	// Context is the per-role composition of the working history at this
	// iteration — drives the cockpit's context graph. Nil when unreported.
	Context *runner.ContextBreakdown
}

// PlanUpdatedMsg carries the structured plan the agent maintains via the
// update_plan tool. Unlike the other messages it does not mirror a runner
// event — the plan flows from the tool's PlanStore straight through the sink
// — but it rides the same pump so it stays ordered with the tool events
// around the update_plan call that produced it.
type PlanUpdatedMsg struct {
	Plan code.Plan
}

// SteerInjectedMsg fires when the runner picks up queued user
// messages between iterations.
type SteerInjectedMsg struct {
	TaskID   string
	Depth    int
	Messages []llm.Message
}

// CompactionAppliedMsg fires when the runner's Compactor returned
// a shrunken history. BytesTrimmed is the engine's own count of
// bytes elided (sum of dropped/elided message bodies); the inner TUI
// converts it into a token-equivalent so the LLM-state gauge can
// drop on byte-only structural trims that leave the message count
// unchanged. Engine labels the compactor that ran ("structural" /
// "summary" / "tiered") so the pane can badge the source.
type CompactionAppliedMsg struct {
	TaskID         string
	Depth          int
	MessagesBefore int
	MessagesAfter  int
	BytesTrimmed   int
	Engine         string
}
