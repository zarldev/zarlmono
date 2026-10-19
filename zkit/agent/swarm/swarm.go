package swarm

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/zsync"
)

type session struct {
	agent       Agent
	state       State
	runID       taskscope.ID
	activations int
	messages    []llm.Message
	pending     []board.ID
	changed     chan struct{}
	sealed      bool
	cancel      context.CancelFunc
	lastError   error
}

// Swarm owns all membership, pending inputs, and activation transitions under mu.
// Lock order is swarm -> board/child source/queue; none calls back into swarm.
// State entries are values. New starts no work; Run owns and joins its workers.
type Swarm struct {
	mu          sync.Mutex
	goal        string
	limits      Limits
	board       *board.Store
	members     map[AgentID]session
	order       []AgentID
	queue       *zsync.Queue[AgentID]
	changed     chan struct{}
	done        chan struct{}
	started     bool
	closed      bool
	cancel      context.CancelCauseFunc
	activations int
	tokens      int
}

// New assembles a goal owner. Call Run once and Close to stop/join it. Goal is a
// host-provided label; independent Swarms never share membership or findings.
func New(goal string, opts ...options.Option[Swarm]) *Swarm {
	s := &Swarm{goal: goal, limits: DefaultLimits(), members: make(map[AgentID]session),
		queue: zsync.NewQueue[AgentID](), changed: make(chan struct{}), done: make(chan struct{})}
	for _, opt := range opts {
		opt(s)
	}
	s.board = board.New(s.limits.Board)
	return s
}

// Add admits a host-constructed agent and schedules its initial assignment.
// Existing posts are announced to late joiners. Duplicate IDs and membership
// limits are runtime conflicts, checked atomically with admission.
func (s *Swarm) Add(agent Agent) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Member{}, ErrClosed
	}
	if _, exists := s.members[agent.ID]; exists {
		return Member{}, ErrConflict
	}
	if len(s.members) >= s.limits.Members {
		return Member{}, ErrCapacity
	}
	agent.Task.Context = llm.CloneMessages(agent.Task.Context)
	agent.Task.Attachments = llm.CloneContentParts(agent.Task.Attachments)
	entry := session{agent: agent, state: States.QUEUED, changed: make(chan struct{}),
		messages: llm.CloneMessages(agent.Task.Context)}
	var cursor board.ID
	for {
		page, _ := s.board.List(cursor, board.MaxPage, "")
		for _, post := range page.Posts {
			entry.pending = append(entry.pending, post.ID)
		}
		if !page.More {
			break
		}
		cursor = page.Next
	}
	s.members[agent.ID] = entry
	s.order = append(s.order, agent.ID)
	_ = s.queue.Push(agent.ID) // Queue closure and admission share s.mu.
	s.signalLocked()
	return Member{owner: s, id: agent.ID}, nil
}

// Run starts bounded workers and remains alive while all members are idle. It
// returns only after cancellation/Close/budget exhaustion and every worker joins.
// A normal idle state is not proof that the shared goal has been completed.
func (s *Swarm) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.started {
		s.mu.Unlock()
		return ErrStarted
	}
	s.started = true
	timed, endTimer := context.WithTimeoutCause(ctx, s.limits.Runtime, ErrBudget)
	runCtx, cancel := context.WithCancelCause(timed)
	s.cancel = cancel
	s.mu.Unlock()
	defer endTimer()
	defer cancel(ErrClosed)

	var workers sync.WaitGroup
	for range s.limits.Workers {
		workers.Go(func() { s.work(runCtx) })
	}
	<-runCtx.Done()
	s.mu.Lock()
	s.closeLocked()
	s.mu.Unlock()
	workers.Wait()
	s.mu.Lock()
	for id, entry := range s.members {
		entry.state = States.STOPPED
		entry.cancel = nil
		s.members[id] = entry
	}
	s.signalLocked()
	close(s.done)
	s.mu.Unlock()
	return context.Cause(runCtx)
}

func (s *Swarm) work(ctx context.Context) {
	for {
		id, err := s.queue.PopContext(ctx)
		if err != nil {
			return
		}
		s.mu.Lock()
		entry := s.members[id]
		if s.closed || ctx.Err() != nil || entry.state != States.QUEUED {
			s.mu.Unlock()
			continue
		}
		if s.activations >= s.limits.Activations || (s.limits.ReportedTokens > 0 && s.tokens >= s.limits.ReportedTokens) {
			s.cancel(ErrBudget)
			s.mu.Unlock()
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		spec := entry.agent.Task
		spec.ID = taskscope.ID(uuid.NewString())
		spec.Context = llm.CloneMessages(entry.messages)
		spec.Attachments = llm.CloneContentParts(spec.Attachments)
		if entry.activations > 0 {
			spec.Prompt = ""
			spec.Attachments = nil
		}
		entry.state, entry.runID, entry.cancel = States.RUNNING, spec.ID, cancel
		entry.sealed = false
		entry.activations++
		s.activations++
		s.members[id] = entry
		s.signalLocked()
		s.mu.Unlock()

		activation := Activation{Spec: spec, runID: spec.ID, Member: Member{owner: s, id: id}}
		result := execute(runCtx, entry.agent.Execute, activation)
		cancel()
		s.mu.Lock()
		entry = s.members[id]
		entry.cancel = nil
		entry.sealed = true
		entry.lastError = result.Err
		if len(result.Messages) != 0 {
			entry.messages = llm.CloneMessages(result.Messages)
		}
		if result.TotalUsage != nil {
			s.tokens += max(result.TotalUsage.TotalTokens, result.TotalUsage.PromptTokens+result.TotalUsage.CompletionTokens)
		}
		if entry.state != States.STOPPED && !s.closed {
			switch {
			case result.Err != nil || result.Reason != runner.TerminalCompleted:
				entry.state = States.SUSPENDED
			case len(entry.pending) > 0:
				entry.state = States.QUEUED
				_ = s.queue.Push(id)
			default:
				entry.state = States.IDLE
			}
		}
		s.members[id] = entry
		s.signalLocked()
		s.mu.Unlock()
	}
}

func execute(ctx context.Context, run Execute, activation Activation) runner.TaskResult {
	var result runner.TaskResult
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				result = runner.TaskResult{ID: activation.runID, Reason: runner.TerminalError,
					Err: fmt.Errorf("swarm execution panic: %v", recovered)}
			}
		}()
		result = run(ctx, activation)
	}()
	return result
}

// Close permanently rejects posts/activations and waits for Run's workers.
// A caller deadline bounds only its wait; Run still owns the underlying drain.
func (s *Swarm) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closeLocked()
		if s.started {
			s.cancel(ErrClosed)
		} else {
			close(s.done)
		}
	}
	s.mu.Unlock()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *Swarm) closeLocked() {
	s.closed = true
	_ = s.queue.Close()
	for id, entry := range s.members {
		entry.state = States.STOPPED
		entry.sealed = true
		s.members[id] = entry
	}
	s.signalLocked()
}

func (s *Swarm) signalLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// WaitIdle waits until no member is queued or executing, without stopping the
// goal. A concurrent later post can immediately activate an idle member again.
func (s *Swarm) WaitIdle(ctx context.Context) error {
	for {
		s.mu.Lock()
		busy := false
		for _, entry := range s.members {
			busy = busy || entry.cancel != nil || entry.state == States.QUEUED
		}
		changed := s.changed
		s.mu.Unlock()
		if !busy {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// Snapshot returns a cloned session view. It never exposes a mutable history alias.
func (s *Swarm) Snapshot(id AgentID) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.members[id]
	if !exists {
		return Snapshot{}, ErrNotFound
	}
	return Snapshot{ID: id, State: entry.state, RunID: entry.runID, Activations: entry.activations,
		Pending: len(entry.pending), Messages: llm.CloneMessages(entry.messages), LastError: entry.lastError}, nil
}

// Stop permanently stops one member and cancels its current execution, if any.
// Close or WaitIdle joins execution; Stop itself never blocks a publishing tool.
func (s *Swarm) Stop(id AgentID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.members[id]
	if !exists {
		return ErrNotFound
	}
	entry.state = States.STOPPED
	entry.sealed = true
	if entry.cancel != nil {
		entry.cancel()
	}
	s.members[id] = entry
	s.signalLocked()
	return nil
}
