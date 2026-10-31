package teasink

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Sink implements [runner.EventSink] by translating each event to
// the matching tea.Msg type and forwarding to the configured send
// function.
// Callback contexts are not retained in UI messages. Cancellation does not
// suppress delivery, so buffered text and terminal events can still settle.
//
// The send function is held atomically so it can be swapped at
// runtime (e.g. between testSend and program.Send). nil send
// silently drops messages — useful before the program is wired.
//
// # Content coalescing
//
// Streaming LLMs emit content chunks at ~100-300/s. Forwarding each
// chunk as its own tea.Msg pushes that many Update + View cycles
// through bubbletea's single-goroutine loop, which back-pressures
// keystrokes — typing feels laggy mid-stream. The Sink coalesces
// consecutive OnContent calls for the same (TaskID, Depth) into a
// single batched [ContentMsg] dispatched on a [coalesceWindow]
// tick. All non-content events first force-flush the buffer so
// "tool started" / "completed" never overtake their preceding
// chunks in the model's message order.
//
// Coalescing is invisible to the model: a 300-chunk burst still
// applies every byte, just in ~5 batched dispatches instead of 300.
//
// # Pump goroutine — non-blocking forwarding
//
// `tea.Program.Send` writes to bubbletea's internal channel and
// blocks while the loop is mid-Update / mid-View. Before this pump,
// every runner-goroutine publish blocked on Send during slow frames,
// which back-pressured into the agent_spawn path (each sub-agent
// runs synchronously on a parent tool-dispatch goroutine) and froze
// the UI under sub-agent storms.
//
// The pump decouples the runner from bubbletea through a bounded FIFO. Queued
// cumulative previews can replace obsolete previews until an intervening event
// fences them. Lifecycle/content events remain reliable and ordered; sustained
// overrun applies backpressure rather than losing calls.
type Sink struct {
	runner.NopSink // future-proof: new event methods inherit no-op until we override

	send atomic.Pointer[sendFunc]

	// Coalesce state — guarded by mu.
	mu       sync.Mutex
	pending  map[contentKey]pendingContent // accumulated delta and known item identity per (TaskID, Depth)
	keyOrder []contentKey                  // FIFO so flushed dispatches preserve arrival order
	timer    *time.Timer                   // armed on first chunk; cleared on flush

	pendingToolOutputs map[toolOutputKey]ToolOutputMsg
	toolOutputOrder    []toolOutputKey
	sealedToolOutputs  map[toolOutputKey]struct{}
	sealedToolOrder    []toolOutputKey

	// Pump state. queue owns the bounded hand-off FIFO; the pump
	// dequeues values and calls the current send function without a queue lock.
	// stop signals the pump to exit. started gates pump creation
	// done closes when the owned pump goroutine exits; closeOnce protects
	// Close from double-close panic.
	queue     pumpQueue
	stop      chan struct{}
	done      chan struct{}
	started   atomic.Bool
	closeOnce sync.Once

	// overflows counts dispatches that had to wait for FIFO capacity. Lifecycle
	// events are never dropped; superseded cumulative previews do not consume
	// additional slots. A nonzero count indicates sustained consumer lag.
	overflows atomic.Int64
}

// coalesceWindow is the maximum delay between a chunk arriving and
// its batched dispatch reaching the bubbletea loop. 16ms ≈ 60fps —
// the user can't perceive a delay shorter than one rendered frame,
// but the loop sees up to a 60× reduction in message volume during
// fast streaming.
const coalesceWindow = 16 * time.Millisecond

// CoalesceWindow returns the maximum delay before buffered content is dispatched.
// It is exposed so callers can synchronize virtual-time tests with Sink behavior.
func CoalesceWindow() time.Duration { return coalesceWindow }

// pumpBuffer is the capacity of the FIFO between dispatch and the
// pump goroutine. Sized to absorb a multi-second sub-agent burst
// without stalling the runner: at ~200 events/s across three
// parallel sub-agents (each ~50 tool events / iteration for ~10
// iterations) a 30-second burst is well under 4k. Larger drains
// memory unnecessarily; smaller risks blocking the runner during
// View-slow windows.
const pumpBuffer = 4096

// contentKey identifies a streaming target. Chunks for different
// tasks (root vs agent_spawn recursion) or different depths merge
// separately so a sub-agent's stream doesn't contaminate the
// parent's.
type contentKey struct {
	TaskID string
	Depth  int
}

type pendingContent struct {
	delta              string
	contentOutputIndex *int
}

type sendFunc func(tea.Msg)

// New constructs a Sink. send may be nil — set later via SetSend.
// Once SetSend has been called with a non-nil function, accepted events
// deliver through the pump. Obsolete queued tool previews may be superseded.
func New(send func(tea.Msg)) *Sink {
	s := &Sink{
		pending:            make(map[contentKey]pendingContent),
		pendingToolOutputs: make(map[toolOutputKey]ToolOutputMsg),
		sealedToolOutputs:  make(map[toolOutputKey]struct{}),
		queue:              newPumpQueue(),
		stop:               make(chan struct{}),
		done:               make(chan struct{}),
	}
	if send != nil {
		s.SetSend(send)
	}
	return s
}

// SetSend swaps the send function atomically. Safe to call from
// any goroutine; safe to call concurrently with event delivery.
// The first non-nil SetSend starts the pump goroutine; subsequent
// swaps just rotate the send target.
func (s *Sink) SetSend(send func(tea.Msg)) {
	if send == nil {
		s.send.Store(nil)
		return
	}
	fn := sendFunc(send)
	s.send.Store(&fn)
	s.startPump()
}

// Close shuts down the pump goroutine and waits for it to exit. Idempotent.
// Pending messages are intentionally discarded because Close runs only after
// the bubbletea program has exited; callers that need delivery use [Sink.Drain]
// before program teardown. Subsequent event methods silently drop their input.
func (s *Sink) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.queue.close()
		if !s.started.Load() {
			close(s.done)
		}
	})
	<-s.done
}

func (s *Sink) startPump() {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	go s.pump()
}

// pump drains s.queue and forwards each msg to the current send
// function. Exits on s.stop. A panic in send (send-on-closed-channel
// during program teardown) is recovered so a single bad dispatch
// can't kill the pump and back up subsequent events.
func (s *Sink) pump() {
	defer close(s.done)
	for !s.stopped() {
		msg, changed := s.queue.pop()
		if msg != nil {
			if !s.stopped() {
				s.deliver(msg)
			}
			continue
		}
		select {
		case <-s.stop:
			return
		case <-changed:
		}
	}
}

func (s *Sink) deliver(msg tea.Msg) {
	defer s.recoverDelivery()
	// Barriers are pump-only sentinels — close the ack and stop;
	// the send function never sees them.
	if b, ok := msg.(barrierMsg); ok {
		close(b.ack)
		return
	}
	p := s.send.Load()
	if p == nil {
		return
	}
	(*p)(msg)
}

// recoverDelivery contains a panic from the send function so one bad
// dispatch can't kill the pump and back up every later event. The only
// panic the pump expects is a send on bubbletea's internal channel
// after the program closed it during teardown. That panic is an
// unexported runtime error with no sentinel to match, so we identify it
// by context rather than by message: once Close has closed s.stop we
// are shutting down and the race is benign — swallow it. A panic while
// still running is a real bug (a malformed msg, a nil deref in send):
// log it with the stack so it surfaces instead of vanishing, then let
// the pump carry on.
func (s *Sink) recoverDelivery() {
	r := recover()
	if r == nil {
		return
	}
	select {
	case <-s.stop:
		return // teardown race — expected, benign
	default:
	}
	slog.Error("teasink: recovered panic delivering message",
		"panic", r, "stack", string(debug.Stack()))
}

// dispatch enqueues msg for the pump goroutine. Drops the message
// when no send is wired (pre-Run / post-Close); blocks only if the
// pump buffer is full (sustained overrun — rare under normal use,
// and counted via [Sink.Overflows] when it happens).
func (s *Sink) dispatch(msg tea.Msg) {
	if !s.started.Load() {
		return
	}
	counted := false
	for !s.stopped() {
		accepted, changed := s.queue.tryPush(msg)
		if accepted {
			return
		}
		if !counted {
			s.overflows.Add(1)
			counted = true
		}
		select {
		case <-s.stop:
			return
		case <-changed:
		}
	}
}

// Overflows returns the number of times [dispatch] found the pump
// channel full and had to fall back to a blocking send. Non-zero
// means the bubbletea loop has fallen behind enough that the
// [pumpBuffer]-deep channel filled — the diagnostic signal the
// freeze fix was designed to surface. Safe to read from any
// goroutine.
func (s *Sink) Overflows() int64 {
	return s.overflows.Load()
}

// --- runner.EventSink impl ---

// OnContent buffers the chunk and arms a flush timer. The first
// chunk in a window starts the timer; subsequent chunks just
// append. The timer fires once, dispatches the merged ContentMsg,
// and clears so the next burst arms a fresh window.
func (s *Sink) OnContent(ctx context.Context, e runner.Content) {
	if e.Delta == "" {
		return
	}
	key := contentKey{TaskID: string(e.TaskID), Depth: e.Depth}

	s.mu.Lock()
	pending, exists := s.pending[key]
	if exists && e.ContentOutputIndex != nil &&
		(pending.contentOutputIndex == nil || *pending.contentOutputIndex != *e.ContentOutputIndex) {
		s.flushLocked()
		pending = pendingContent{}
		exists = false
	}
	if !exists {
		s.keyOrder = append(s.keyOrder, key)
	}
	pending.delta += e.Delta
	if pending.contentOutputIndex == nil && e.ContentOutputIndex != nil {
		pending.contentOutputIndex = new(*e.ContentOutputIndex)
	}
	s.pending[key] = pending
	if s.timer == nil {
		s.timer = time.AfterFunc(coalesceWindow, s.flush)
	}
	s.mu.Unlock()
}

// OnThinking forwards a reasoning delta straight to the TUI. Unlike content,
// thinking isn't coalesced — it's lower-volume and, for reasoning models,
// arrives as a block before any visible content, so per-delta dispatch keeps
// the reasoning pane live without a flush window.
func (s *Sink) OnThinking(ctx context.Context, e runner.Thinking) {
	if e.Delta == "" {
		return
	}
	s.dispatch(ThinkingMsg{TaskID: string(e.TaskID), Depth: e.Depth, Delta: e.Delta})
}

// Flush is the public synchronous flush — useful for tests, for
// shutdown paths that want to guarantee the last chunk reaches the
// model, and for callers that want to bypass the coalesce window
// (e.g. when a turn cancels and the pending chunks should land
// before the cancel-notice does). Drains the coalesce buffer; the
// pump then delivers asynchronously. Use [Sink.Drain] when a test
// needs to wait for actual delivery to the send function.
// Idempotent; safe to call from any goroutine.
func (s *Sink) Flush() { s.flush() }

// Drain flushes pending content and waits for all previously queued lifecycle
// events and non-superseded previews to reach the send function (or for Close).
// Its FIFO barrier prevents later previews from replacing earlier ones across
// the drain boundary. Call before program teardown when delivery is required.
func (s *Sink) Drain() {
	s.Flush()
	if !s.started.Load() {
		return
	}
	ack := make(chan struct{})
	s.dispatch(barrierMsg{ack: ack})
	select {
	case <-ack:
	case <-s.stop:
	}
}

// barrierMsg is a pump-only sentinel used by Drain to wait until
// every preceding message has been delivered. Recognised in deliver;
// never reaches the send function.
type barrierMsg struct{ ack chan struct{} }

// flush drains the pending buffer and dispatches one ContentMsg per
// (TaskID, Depth) accumulated. Safe to call from the timer or from
// another event handler that needs to preserve ordering before its
// own dispatch.
func (s *Sink) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
}

func (s *Sink) flushLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	// Keep the flush serialized through enqueue. Otherwise a timer could
	// detach pending content, unlock, and enqueue it after an event barrier
	// that observed an empty buffer. The pump does not acquire this lock.
	for _, k := range s.keyOrder {
		pending := s.pending[k]
		var outputIndex *int
		if pending.contentOutputIndex != nil {
			outputIndex = new(*pending.contentOutputIndex)
		}
		s.dispatch(ContentMsg{
			TaskID:             k.TaskID,
			Depth:              k.Depth,
			Delta:              pending.delta,
			ContentOutputIndex: outputIndex,
		})
	}
	s.keyOrder = nil
	clear(s.pending)
	for _, key := range s.toolOutputOrder {
		s.dispatch(s.pendingToolOutputs[key])
	}
	s.toolOutputOrder = nil
	clear(s.pendingToolOutputs)
}

// OnToolStarted flushes pending content first so "tool started"
// never appears before the chunks that preceded it, then forwards
// as ToolStartedMsg.
func (s *Sink) OnToolStarted(ctx context.Context, e runner.ToolStarted) {
	s.startToolOutput(toolOutputKeyFor(
		string(e.TaskID), e.Depth, e.ExecutionID, e.ToolID, e.ToolName,
		e.ParentExecutionID, e.ParentToolID, e.Sequence,
	), ToolStartedMsg{
		TaskID:            string(e.TaskID),
		Depth:             e.Depth,
		ExecutionID:       e.ExecutionID,
		ToolID:            e.ToolID,
		ToolName:          e.ToolName,
		Parameters:        tools.CloneParameters(e.Parameters),
		RawArguments:      e.RawArguments,
		ParentToolID:      e.ParentToolID,
		ParentExecutionID: e.ParentExecutionID,
		Sequence:          e.Sequence,
	})
}

// OnWorkspaceWaitStarted flushes pending content, then forwards the workspace wait event.
func (s *Sink) OnWorkspaceWaitStarted(ctx context.Context, e runner.WorkspaceWaitStarted) {
	s.flush()
	s.dispatch(WorkspaceWaitStartedMsg{
		TaskID: string(e.TaskID), Depth: e.Depth, ExecutionID: e.ExecutionID, ToolID: e.ToolID, ToolName: e.ToolName,
		Access: e.Access, Paths: e.Paths, BlockerCount: e.BlockerCount,
		ParentToolID: e.ParentToolID, ParentExecutionID: e.ParentExecutionID, Sequence: e.Sequence,
	})
}

// OnToolCompleted forwards as ToolCompletedMsg.
func (s *Sink) OnToolCompleted(ctx context.Context, e runner.ToolCompleted) {
	s.settleToolOutput(toolOutputKeyFor(
		string(e.TaskID), e.Depth, e.ExecutionID, e.ToolID, e.ToolName,
		e.ParentExecutionID, e.ParentToolID, e.Sequence,
	), ToolCompletedMsg{
		TaskID:            string(e.TaskID),
		Depth:             e.Depth,
		ExecutionID:       e.ExecutionID,
		ToolID:            e.ToolID,
		ToolName:          e.ToolName,
		Result:            e.Result,
		FormattedResult:   e.FormattedResult,
		Parts:             llm.CloneContentParts(e.Parts),
		Effects:           cloneEffects(e.Effects),
		Duration:          e.Duration,
		ParentToolID:      e.ParentToolID,
		ParentExecutionID: e.ParentExecutionID,
		Sequence:          e.Sequence,
	})
}

// OnToolFailed forwards as ToolFailedMsg.
func (s *Sink) OnToolFailed(ctx context.Context, e runner.ToolFailed) {
	s.settleToolOutput(toolOutputKeyFor(
		string(e.TaskID), e.Depth, e.ExecutionID, e.ToolID, e.ToolName,
		e.ParentExecutionID, e.ParentToolID, e.Sequence,
	), ToolFailedMsg{
		TaskID:            string(e.TaskID),
		Depth:             e.Depth,
		ExecutionID:       e.ExecutionID,
		ToolID:            e.ToolID,
		ToolName:          e.ToolName,
		Error:             e.Error,
		RawOutput:         e.RawOutput,
		Parts:             llm.CloneContentParts(e.Parts),
		Kind:              e.Kind, // flat classification only; e.Err is not forwarded to the UI
		Abandoned:         e.Abandoned,
		ParentToolID:      e.ParentToolID,
		ParentExecutionID: e.ParentExecutionID,
		Sequence:          e.Sequence,
		Effects:           cloneEffects(e.Effects),
		Duration:          e.Duration,
	})
}

// OnWorkspaceWaitEnded flushes pending content, then forwards the workspace wait event.
func (s *Sink) OnWorkspaceWaitEnded(ctx context.Context, e runner.WorkspaceWaitEnded) {
	s.flush()
	s.dispatch(WorkspaceWaitEndedMsg{
		TaskID: string(e.TaskID), Depth: e.Depth, ExecutionID: e.ExecutionID, ToolID: e.ToolID, ToolName: e.ToolName,
		Outcome: e.Outcome, Duration: e.Duration,
		ParentToolID: e.ParentToolID, ParentExecutionID: e.ParentExecutionID, Sequence: e.Sequence,
	})
}

// OnConversationStarted forwards as ConversationStartedMsg.
func (s *Sink) OnConversationStarted(ctx context.Context, e runner.ConversationStarted) {
	s.flush()
	s.dispatch(ConversationStartedMsg{
		TaskID:            string(e.TaskID),
		Depth:             e.Depth,
		Prompt:            e.Prompt,
		ParentToolCallID:  e.ParentToolCallID,
		ParentExecutionID: e.ParentExecutionID,
		AgentName:         e.AgentName,
		Provider:          e.Provider,
		Model:             e.Model,
	})
}

// OnConversationEnded forwards as ConversationEndedMsg.
func (s *Sink) OnConversationEnded(ctx context.Context, e runner.ConversationEnded) {
	s.flush()
	s.dispatch(ConversationEndedMsg{
		TaskID:            string(e.TaskID),
		Depth:             e.Depth,
		Reason:            e.Reason,
		Cause:             e.Cause,
		Error:             e.Error,
		RateLimit:         cloneRateLimitError(e.RateLimit),
		Duration:          e.Duration,
		Iterations:        e.Iterations,
		TotalUsage:        e.TotalUsage,
		ParentToolCallID:  e.ParentToolCallID,
		ParentExecutionID: e.ParentExecutionID,
	})
}

// OnIterationCompleted forwards as IterationCompletedMsg. Fires once
// per iteration within a Run after content streaming and tool
// dispatch settle. flush() drains any coalesced content so the
// iteration boundary lands after every chunk that belongs to it.
func (s *Sink) OnIterationCompleted(ctx context.Context, e runner.IterationCompleted) {
	s.flush()
	s.dispatch(IterationCompletedMsg{
		TaskID:                     string(e.TaskID),
		Depth:                      e.Depth,
		Iter:                       e.Iter,
		Usage:                      e.Usage,
		Delta:                      e.Delta,
		Context:                    e.Context,
		ToolSurface:                e.ToolSurface,
		RequestPreparationDuration: e.RequestPreparationDuration,
		ToolDispatchDuration:       e.ToolDispatchDuration,
	})
}

// OnSteerInjected forwards as SteerInjectedMsg.
func (s *Sink) OnSteerInjected(ctx context.Context, e runner.SteerInjected) {
	s.flush()
	s.dispatch(SteerInjectedMsg{
		TaskID:   string(e.TaskID),
		Depth:    e.Depth,
		Messages: cloneMessages(e.Messages),
	})
}

// OnCompactionApplied forwards as CompactionAppliedMsg.
func (s *Sink) OnCompactionApplied(ctx context.Context, e runner.CompactionApplied) {
	s.flush()
	s.dispatch(CompactionAppliedMsg{
		TaskID:         string(e.TaskID),
		Depth:          e.Depth,
		MessagesBefore: e.MessagesBefore,
		MessagesAfter:  e.MessagesAfter,
		BytesTrimmed:   e.BytesTrimmed,
		Engine:         e.Engine,
	})
}

func cloneEffects(effects []tools.Effect) []tools.Effect {
	if effects == nil {
		return nil
	}
	cloned := make([]tools.Effect, len(effects))
	for i, effect := range effects {
		cloned[i] = effect
		if effect.File != nil {
			file := *effect.File
			cloned[i].File = &file
		}
		if effect.Process != nil {
			process := *effect.Process
			cloned[i].Process = &process
		}
	}
	return cloned
}

func cloneRateLimitError(rateLimit *llm.RateLimitError) *llm.RateLimitError {
	if rateLimit == nil {
		return nil
	}
	cloned := *rateLimit
	return &cloned
}

func cloneMessages(messages []llm.Message) []llm.Message {
	if messages == nil {
		return nil
	}
	return llm.CloneMessages(messages)
}

// OnDiagnostic intentionally keeps recovery diagnostics out of the transcript.
func (s *Sink) OnDiagnostic(context.Context, runner.Diagnostic) {}

// PlanUpdated forwards a structured plan update from the update_plan tool's
// PlanStore. Not a runner event — the tool calls it directly — but it flushes
// pending content first so the plan lands ordered with the surrounding tool
// events rather than overtaking the streamed text before them.
func (s *Sink) PlanUpdated(taskID string, plan code.Plan) {
	s.flush()
	s.dispatch(PlanUpdatedMsg{TaskID: taskID, Plan: plan})
}

// PromptDiagnostics forwards prompt resolution diagnostics. Not a runner event;
// live prompt resolution calls it directly before a provider request.
func (s *Sink) PromptDiagnostics(diags []string) {
	if len(diags) == 0 {
		return
	}
	s.flush()
	s.dispatch(PromptDiagnosticsMsg{Diagnostics: append([]string(nil), diags...)})
}

// ModeChanged delivers an already-applied workflow transition in event order.
func (s *Sink) ModeChanged(change engine.ModeChanged) {
	s.flush()
	s.dispatch(ModeChangedMsg(change))
}
