package teasink

import (
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Sink implements [runner.EventSink] by translating each event to
// the matching tea.Msg type and forwarding to the configured send
// function.
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
// which back-pressured into the spawn_agent path (each sub-agent
// runs synchronously on a parent tool-dispatch goroutine) and froze
// the UI under sub-agent storms.
//
// The pump decouples the runner from bubbletea: dispatch pushes into
// a deep buffered channel (`pumpBuffer`); a single pump goroutine
// drains the channel into `tea.Program.Send` at whatever rate the
// loop accepts. Short bursts ride in the buffer without ever stalling
// the runner. Sustained overrun blocks dispatch (channel-full) — the
// pump preserves event ordering and never silently drops, so a
// genuine throughput problem surfaces as backpressure rather than
// lost tool calls.
type Sink struct {
	runner.NopSink // future-proof: new event methods inherit no-op until we override

	send atomic.Pointer[sendFunc]

	// Coalesce state — guarded by mu.
	mu       sync.Mutex
	pending  map[contentKey]string // accumulated delta per (TaskID, Depth)
	keyOrder []contentKey          // FIFO so flushed dispatches preserve arrival order
	timer    *time.Timer           // armed on first chunk; cleared on flush

	// Pump state. msgs is the buffered hand-off channel; the pump
	// goroutine reads from msgs and calls the current send function.
	// stop signals the pump to exit. started gates pump creation
	// (set true on the first non-nil SetSend), closeOnce protects
	// Close from double-close panic.
	msgs      chan tea.Msg
	stop      chan struct{}
	started   atomic.Bool
	closeOnce sync.Once

	// overflows counts how often dispatch found the pump channel
	// full and had to fall back to a blocking send — the only path
	// the runner goroutine can actually stall on after the pump
	// rework. Events are never dropped (UI correctness depends on
	// every ToolStarted/Completed reaching Update), so this is a
	// pure diagnostic. Non-zero under normal use means the
	// bubbletea loop has fallen behind enough that the
	// [pumpBuffer]-deep channel filled — the signal the freeze fix
	// was designed to make observable.
	overflows atomic.Int64
}

// coalesceWindow is the maximum delay between a chunk arriving and
// its batched dispatch reaching the bubbletea loop. 16ms ≈ 60fps —
// the user can't perceive a delay shorter than one rendered frame,
// but the loop sees up to a 60× reduction in message volume during
// fast streaming.
const coalesceWindow = 16 * time.Millisecond

// pumpBuffer is the depth of the channel between dispatch and the
// pump goroutine. Sized to absorb a multi-second sub-agent burst
// without stalling the runner: at ~200 events/s across three
// parallel sub-agents (each ~50 tool events / iteration for ~10
// iterations) a 30-second burst is well under 4k. Larger drains
// memory unnecessarily; smaller risks blocking the runner during
// View-slow windows.
const pumpBuffer = 4096

// contentKey identifies a streaming target. Chunks for different
// tasks (root vs spawn_agent recursion) or different depths merge
// separately so a sub-agent's stream doesn't contaminate the
// parent's.
type contentKey struct {
	TaskID string
	Depth  int
}

type sendFunc func(tea.Msg)

// New constructs a Sink. send may be nil — set later via SetSend.
// Once SetSend has been called with a non-nil function, every
// subsequent event delivers to that function via the pump goroutine.
func New(send func(tea.Msg)) *Sink {
	s := &Sink{
		pending: make(map[contentKey]string),
		msgs:    make(chan tea.Msg, pumpBuffer),
		stop:    make(chan struct{}),
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

// Close shuts down the pump goroutine. Idempotent. Callers should
// invoke Close after the bubbletea program has exited so the pump
// can drain any remaining messages and exit cleanly. After Close,
// subsequent event methods silently drop their input.
func (s *Sink) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
	})
}

func (s *Sink) startPump() {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	go s.pump()
}

// pump drains s.msgs and forwards each msg to the current send
// function. Exits on s.stop. A panic in send (send-on-closed-channel
// during program teardown) is recovered so a single bad dispatch
// can't kill the pump and back up subsequent events.
func (s *Sink) pump() {
	for {
		select {
		case <-s.stop:
			return
		case msg := <-s.msgs:
			s.deliver(msg)
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
	// Check stop first so post-Close dispatch is a clean no-op
	// rather than blocking on a channel the pump's no longer
	// reading.
	select {
	case <-s.stop:
		return
	default:
	}
	// Try non-blocking first. If the buffer has room, dispatch is
	// fully decoupled from the bubbletea loop — the runner never
	// stalls. If the buffer's full, the loop has fallen behind:
	// count the overflow so operators can see it via [Overflows],
	// then fall back to a blocking send so we don't drop the event
	// (every ToolStarted / Completed is load-bearing for UI
	// correctness; dropping would manifest as missing rows).
	select {
	case s.msgs <- msg:
		return
	default:
	}
	s.overflows.Add(1)
	select {
	case s.msgs <- msg:
	case <-s.stop:
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
func (s *Sink) OnContent(e runner.Content) {
	if e.Delta == "" {
		return
	}
	key := contentKey{TaskID: string(e.TaskID), Depth: e.Depth}

	s.mu.Lock()
	if _, ok := s.pending[key]; !ok {
		s.keyOrder = append(s.keyOrder, key)
	}
	s.pending[key] += e.Delta
	armed := s.timer != nil
	if !armed {
		s.timer = time.AfterFunc(coalesceWindow, s.flush)
	}
	s.mu.Unlock()
}

// OnThinking forwards a reasoning delta straight to the TUI. Unlike content,
// thinking isn't coalesced — it's lower-volume and, for reasoning models,
// arrives as a block before any visible content, so per-delta dispatch keeps
// the reasoning pane live without a flush window.
func (s *Sink) OnThinking(e runner.Thinking) {
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

// Drain blocks until every message dispatched so far has been
// delivered to the send function. Intended for tests and shutdown:
// after Drain returns, the recording send is guaranteed to have
// observed all prior events. Calls Flush first to push any pending
// coalesced content into the pump.
//
// Implementation: enqueues a barrier sentinel via the same channel
// the pump drains and waits on its ack. Because the channel is FIFO
// and the pump processes one message at a time, the ack only fires
// once every prior message has been delivered.
func (s *Sink) Drain() {
	s.Flush()
	if !s.started.Load() {
		return
	}
	ack := make(chan struct{})
	select {
	case s.msgs <- barrierMsg{ack: ack}:
	case <-s.stop:
		return
	}
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
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	// Snapshot under lock so concurrent OnContent calls can't see a
	// half-drained map. Dispatch outside the lock — dispatch may
	// block on the bubbletea send and we don't want it to hold the
	// mutex for the duration.
	keys := s.keyOrder
	merged := s.pending
	s.keyOrder = nil
	s.pending = make(map[contentKey]string, len(merged))
	s.mu.Unlock()

	for _, k := range keys {
		s.dispatch(ContentMsg{
			TaskID: k.TaskID,
			Depth:  k.Depth,
			Delta:  merged[k],
		})
	}
}

// OnToolStarted flushes pending content first so "tool started"
// never appears before the chunks that preceded it, then forwards
// as ToolStartedMsg.
func (s *Sink) OnToolStarted(e runner.ToolStarted) {
	s.flush()
	s.dispatch(ToolStartedMsg{
		TaskID:     string(e.TaskID),
		Depth:      e.Depth,
		ToolID:     e.ToolID,
		ToolName:   e.ToolName,
		Parameters: e.Parameters,
	})
}

// OnToolCompleted forwards as ToolCompletedMsg.
func (s *Sink) OnToolCompleted(e runner.ToolCompleted) {
	s.flush()
	s.dispatch(ToolCompletedMsg{
		TaskID:          string(e.TaskID),
		Depth:           e.Depth,
		ToolID:          e.ToolID,
		ToolName:        e.ToolName,
		Result:          e.Result,
		FormattedResult: e.FormattedResult,
		Effects:         e.Effects,
		Duration:        e.Duration,
	})
}

// OnToolFailed forwards as ToolFailedMsg.
func (s *Sink) OnToolFailed(e runner.ToolFailed) {
	s.flush()
	s.dispatch(ToolFailedMsg{
		TaskID:    string(e.TaskID),
		Depth:     e.Depth,
		ToolID:    e.ToolID,
		ToolName:  e.ToolName,
		Error:     e.Error,
		Kind:      e.Kind, // flat classification only; e.Err is not forwarded to the UI
		Abandoned: e.Abandoned,
		Effects:   e.Effects,
		Duration:  e.Duration,
	})
}

// OnConversationStarted forwards as ConversationStartedMsg.
func (s *Sink) OnConversationStarted(e runner.ConversationStarted) {
	s.flush()
	s.dispatch(ConversationStartedMsg{
		TaskID:           string(e.TaskID),
		Depth:            e.Depth,
		Prompt:           e.Prompt,
		ParentToolCallID: e.ParentToolCallID,
		AgentName:        e.AgentName,
	})
}

// OnConversationEnded forwards as ConversationEndedMsg.
func (s *Sink) OnConversationEnded(e runner.ConversationEnded) {
	s.flush()
	s.dispatch(ConversationEndedMsg{
		TaskID:           string(e.TaskID),
		Depth:            e.Depth,
		Reason:           e.Reason,
		Error:            e.Error,
		Duration:         e.Duration,
		Iterations:       e.Iterations,
		TotalUsage:       e.TotalUsage,
		ParentToolCallID: e.ParentToolCallID,
	})
}

// OnIterationCompleted forwards as IterationCompletedMsg. Fires once
// per iteration within a Run after content streaming and tool
// dispatch settle. flush() drains any coalesced content so the
// iteration boundary lands after every chunk that belongs to it.
func (s *Sink) OnIterationCompleted(e runner.IterationCompleted) {
	s.flush()
	s.dispatch(IterationCompletedMsg{
		TaskID:  string(e.TaskID),
		Depth:   e.Depth,
		Iter:    e.Iter,
		Usage:   e.Usage,
		Delta:   e.Delta,
		Context: e.Context,
	})
}

// OnSteerInjected forwards as SteerInjectedMsg.
func (s *Sink) OnSteerInjected(e runner.SteerInjected) {
	s.flush()
	s.dispatch(SteerInjectedMsg{
		TaskID:   string(e.TaskID),
		Depth:    e.Depth,
		Messages: e.Messages,
	})
}

// OnCompactionApplied forwards as CompactionAppliedMsg.
func (s *Sink) OnCompactionApplied(e runner.CompactionApplied) {
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

// PlanUpdated forwards a structured plan update from the update_plan tool's
// PlanStore. Not a runner event — the tool calls it directly — but it flushes
// pending content first so the plan lands ordered with the surrounding tool
// events rather than overtaking the streamed text before them.
func (s *Sink) PlanUpdated(p code.Plan) {
	s.flush()
	s.dispatch(PlanUpdatedMsg{Plan: p})
}
