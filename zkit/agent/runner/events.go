package runner

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Event payloads. Flat structs — exhaustiveness comes from the
// interface methods on EventSink, not from a discriminated union.

// Content is a streamed assistant-message delta.
type Content struct {
	TaskID taskscope.ID
	Depth  int
	Delta  string
}

// Thinking is a streamed reasoning delta — extended-thinking /
// chain-of-thought tokens, carried separately from visible Content so a UI
// can render them in a dedicated surface. Providers that inline reasoning as
// <think> tags route it through Content instead.
type Thinking struct {
	TaskID taskscope.ID
	Depth  int
	Delta  string
}

// ToolStarted fires when the runner dispatches a tool call.
type ToolStarted struct {
	TaskID     taskscope.ID
	Depth      int
	ToolID     string
	ToolName   string
	Parameters map[string]any
}

// ToolCompleted fires when a tool call returns successfully.
type ToolCompleted struct {
	TaskID          taskscope.ID
	Depth           int
	ToolID          string
	ToolName        string
	Result          any
	FormattedResult string
	Effects         []tools.Effect
	Duration        time.Duration
}

// ToolFailed fires when a tool call errors or reports failure.
type ToolFailed struct {
	TaskID   taskscope.ID
	Depth    int
	ToolID   string
	ToolName string
	// Error is the user-facing failure message — safe to surface in a UI.
	Error string
	// Err is the underlying typed error (the result's *tools.Error, a
	// context error, etc.), carrying Op / Reason / Wrapped for sinks that
	// want errors.As/Is introspection or structured logging. NOT forwarded
	// to the UI — the flat Error string is what's user-facing — so internal
	// error detail doesn't leak into the transcript. Nil on legacy paths.
	Err error
	// Kind classifies the failure (validation / not_found / transient /
	// fatal / …) from the tool result's typed Err.Kind, so consumers render
	// or react to the class rather than substring-matching Error.
	Kind tools.Kind
	// Abandoned is true when the failure is the runner giving up on a tool
	// that blew its per-tool time budget while still in flight: the runner
	// stopped waiting and reported a timeout, but the tool's goroutine may
	// still be running and mutating state. Distinguishes "the tool failed"
	// and "the tool timed out and stopped" from "the tool timed out and
	// was abandoned with side effects possibly still in flight" — the one
	// a consumer may want to surface or alert on.
	Abandoned bool
	Effects   []tools.Effect
	Duration  time.Duration
}

// ConversationStarted marks the start of a Run. Prompt carries the
// task's seed prompt — the same string the caller handed to Run via
// TaskSpec.Prompt. The TUI stack pane uses it to label each frame
// with what the (sub-)agent is actually working on; without it the
// pane could only show depth + opaque task ID.
//
// ParentToolCallID identifies the tool call that initiated this Run
// (set by spawn-agent for sub-agents). Empty for top-level Runs. The
// TUI uses it to attribute a sub-agent's events back to the exact
// parent tool call that spawned it — critical for parallel
// spawn_agent dispatch where multiple sub-agents are in flight and
// can't be distinguished by task ID + Depth alone.
type ConversationStarted struct {
	TaskID           taskscope.ID
	Depth            int
	Prompt           string
	ParentToolCallID string
	AgentName        string
}

// ConversationEnded is the single terminal event for a Run — it fires
// exactly once per Run, for every way a Run can end. Reason classifies
// the outcome (completed, max_iterations, cancelled, or error); Error
// carries the failure message when Reason is error, and is empty
// otherwise. Consumers switch on Reason rather than on which method
// fired, so "the turn finished" can't be mistaken for "the turn
// succeeded" — a completed answer, a truncated max-iterations turn, a
// user cancellation, and an unrecoverable fault all arrive here.
//
// TotalUsage / Iterations / ParentToolCallID let subscribers attribute
// the full token spend of a Run back to its initiator without having to
// wait for the spawning tool's ToolCompleted (which only carries the
// child's text summary, not its usage). Top-level Runs leave
// ParentToolCallID empty; sub-agents echo the field they received on the
// matching ConversationStarted.
type ConversationEnded struct {
	TaskID taskscope.ID
	Depth  int
	Reason TerminalReason
	Error  string
	// RateLimit is set when the terminal error is (or wraps) a
	// *llm.RateLimitError, so subscribers can render structured timing
	// (retry-after / reset / permanent-quota) without re-parsing Error.
	// Nil for every non-rate-limit outcome.
	RateLimit        *llm.RateLimitError
	Duration         time.Duration
	Iterations       int
	TotalUsage       *llm.Usage
	ParentToolCallID string
}

// IterationCompleted fires at the end of each iteration of a Run, after
// content streaming and tool dispatch settle. Usage and Delta split the
// two things a subscriber can want from token accounting — occupancy and
// flow. Both are advisory: not every provider emits usage on every
// stream, so a nil Delta is normal.
type IterationCompleted struct {
	TaskID taskscope.ID
	Depth  int
	Iter   int
	// Usage is the most recent usage observed across the Run — a proxy
	// for current context occupancy (what token gauges and the
	// PressureGated compaction gate read). Once any iteration has
	// reported usage it never regresses to nil, even when this
	// iteration's provider dropped usage.
	Usage *llm.Usage
	// Delta is this iteration's own reported usage. Nil when the
	// provider omitted usage on this stream (llama.cpp's openai-compat
	// endpoint is known to drop it on the final chunk). Sum Delta across
	// iterations for per-turn flow; read Usage for occupancy.
	Delta *llm.Usage

	// Context is a per-role byte/message breakdown of the working history
	// at this iteration boundary — enough for a subscriber to draw a
	// context-window composition graph without holding the message slice
	// itself (the runner owns it; the slice would alias and race). Nil
	// when the runner didn't compute one. Advisory, like Usage.
	Context *ContextBreakdown
}

// ContextBreakdown is the composition of a Run's working history by role,
// in raw content bytes plus message counts. Tool bytes are further split
// into the load_skill / spawn_agent content that dominates most coding
// sessions, so a subscriber can show "how much of the window is reference
// docs vs delegated summaries vs ordinary tool output". Bytes are raw
// content bytes; a chars/4 token estimate is the subscriber's to make.
type ContextBreakdown struct {
	SystemBytes    int
	UserBytes      int
	AssistantBytes int
	ToolBytes      int

	// SkillBytes / AgentBytes are the load_skill / spawn_agent slices of
	// ToolBytes (ToolBytes - SkillBytes - AgentBytes == "other tool" bytes).
	SkillBytes int
	AgentBytes int

	SystemMsgs    int
	UserMsgs      int
	AssistantMsgs int
	ToolMsgs      int
}

// SteerInjected fires when the runner picks up queued user messages
// from the Steerer between iterations.
type SteerInjected struct {
	TaskID   taskscope.ID
	Depth    int
	Messages []llm.Message
}

// CompactionApplied fires when the runner's Compactor returned a
// shrunken history OR trimmed bytes in place. Carries the
// message-count delta and the per-engine BytesTrimmed report so
// subscribers can show "[compacted: N → M messages, -B bytes]" or
// similar. Engine is the label the compactor returned
// ("structural" / "summary") for UI badging.
type CompactionApplied struct {
	TaskID         taskscope.ID
	Depth          int
	MessagesBefore int
	MessagesAfter  int
	BytesTrimmed   int
	Engine         string
}

// --- Sub-sinks: small, single-purpose ---

// ContentSink observes streamed assistant-message deltas. The
// runner emits one OnContent per chunk that carries a Content
// field (no batching, no joining); subscribers wanting the full
// final message accumulate themselves.
type ContentSink interface {
	OnContent(Content)
}

// ThinkingSink observes streamed reasoning deltas (extended thinking /
// chain-of-thought), kept separate from visible content so a UI can render
// them in a dedicated reasoning surface. One OnThinking per reasoning-bearing
// chunk; subscribers accumulate themselves.
type ThinkingSink interface {
	OnThinking(Thinking)
}

// ToolSink observes tool-call lifecycle. Each call dispatched by
// the runner produces exactly one OnToolStarted and exactly one
// of OnToolCompleted or OnToolFailed.
type ToolSink interface {
	OnToolStarted(ToolStarted)
	OnToolCompleted(ToolCompleted)
	OnToolFailed(ToolFailed)
}

// ConversationSink observes the bookend events around a single
// Run. Started fires once per Run; Ended fires exactly once after it
// (carrying the terminal reason). Sub-agent runs (depth > 0) produce
// their own Started/Ended pairs. OnIterationCompleted fires once per
// iteration *within* a Run, between Started and Ended.
type ConversationSink interface {
	OnConversationStarted(ConversationStarted)
	OnConversationEnded(ConversationEnded)
	OnIterationCompleted(IterationCompleted)
}

// SteerSink observes when the runner picked up queued user
// messages from the Steerer between iterations. Useful for UIs
// that want to render the injected lines in the transcript.
type SteerSink interface {
	OnSteerInjected(SteerInjected)
}

// CompactionSink observes when the runner's Compactor returned
// a shrunken history. MessagesBefore/After let subscribers show
// the size delta to the user.
type CompactionSink interface {
	OnCompactionApplied(CompactionApplied)
}

// EventSink is the composite the runner takes. Adding an event method
// here breaks every full-EventSink implementer until they handle it —
// that's the compile-time exhaustiveness contract. Implementers that
// genuinely want to ignore future events embed NopSink and override
// only what they care about.
//
// # Concurrency
//
// Implementations MUST be safe for concurrent calls. Two scenarios
// reach the sink from multiple goroutines:
//
//   - WithToolConcurrency(N>1) dispatches tool calls in parallel; each
//     goroutine independently fires OnToolStarted / OnToolCompleted /
//     OnToolFailed.
//   - A single Runner servicing concurrent Run calls publishes events
//     for each task on the calling goroutine.
//
// Mutex-guarded slice append, channel send, or atomic counter all work.
// The runner does not synchronise its publishes; the sink is on the
// hook for any internal locking. Consumers that don't want to hand-roll
// that locking can wrap any sink in [SyncSink], which serialises every
// call behind one mutex.
type EventSink interface {
	ContentSink
	ThinkingSink
	ToolSink
	ConversationSink
	SteerSink
	CompactionSink
}

// NopSink satisfies EventSink with no-op methods. Embed when you want
// to opt out of exhaustiveness for a specific consumer.
type NopSink struct{}

// Every NopSink event method discards its event and returns immediately.

func (NopSink) OnContent(Content)                         {}
func (NopSink) OnThinking(Thinking)                       {}
func (NopSink) OnToolStarted(ToolStarted)                 {}
func (NopSink) OnToolCompleted(ToolCompleted)             {}
func (NopSink) OnToolFailed(ToolFailed)                   {}
func (NopSink) OnConversationStarted(ConversationStarted) {}
func (NopSink) OnConversationEnded(ConversationEnded)     {}
func (NopSink) OnIterationCompleted(IterationCompleted)   {}
func (NopSink) OnSteerInjected(SteerInjected)             {}
func (NopSink) OnCompactionApplied(CompactionApplied)     {}
