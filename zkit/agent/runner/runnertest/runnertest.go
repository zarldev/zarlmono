// Package runnertest provides shared test fakes for code that uses
// zkit/agent/runner: a scriptable Client, a recording EventSink, a
// minimal Tool stub, and chunk constructors. Lifted out of runner's
// own test package so downstream consumers (zarlai, third-party
// agent shells) don't have to reinvent them.
//
// Typical use:
//
//	client := runnertest.NewClient([][]llm.CompletionChunk{
//	    {runnertest.ChunkText("done"), runnertest.ChunkDone()},
//	})
//	sink := runnertest.NewSink()
//	r := runner.New(client, registry, runner.WithSink(sink))
//	res, _ := r.Run(ctx, runner.TaskSpec{Prompt: "ping"})
//	if sink.ContentCount() == 0 {
//	    t.Fatal("no content emitted")
//	}
//
// The fakes are deliberately simple — no retry, no rate-limit
// simulation, no streaming-delay shaping. Tests that need those
// build their own.
package runnertest

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// --- Client ---

// Client is a scripted runner.Client. Each call to Complete consumes
// the next entry in turns. Out-of-script calls return an error;
// concurrent Run callers are responsible for sequencing if they
// share a Client.
type Client struct {
	mu    sync.Mutex
	turns [][]llm.CompletionChunk
	calls int
}

// NewClient returns a Client that yields turns[i] on the i-th
// Complete call.
func NewClient(turns [][]llm.CompletionChunk) *Client {
	return &Client{turns: turns}
}

// Complete replays the next scripted turn's chunks in order; once the
// script is exhausted it errors with the call number, so a runaway loop
// fails loudly instead of hanging.
func (c *Client) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	c.mu.Lock()
	if c.calls >= len(c.turns) {
		c.mu.Unlock()
		return nil, fmt.Errorf("runnertest.Client: out of scripted turns (call #%d)", c.calls+1)
	}
	chunks := c.turns[c.calls]
	c.calls++
	c.mu.Unlock()

	return func(yield func(llm.CompletionChunk, error) bool) {
		for _, ch := range chunks {
			if !yield(ch, nil) {
				return
			}
		}
	}, nil
}

// CallCount returns the number of Complete calls made so far. Useful
// for asserting the runner consumed exactly N turns.
func (c *Client) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// --- Chunk constructors ---

// ChunkText returns a CompletionChunk carrying the given content
// (Done: false). Use for streamed-text turns.
func ChunkText(text string) llm.CompletionChunk {
	return llm.CompletionChunk{Content: text}
}

// ChunkToolCall returns a CompletionChunk carrying a single
// function-style tool call. The arguments string is whatever
// JSON the model would have produced.
func ChunkToolCall(id, name, args string) llm.CompletionChunk {
	return llm.CompletionChunk{
		ToolCalls: []llm.ToolCall{{
			ID:   id,
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      name,
				Arguments: args,
			},
		}},
	}
}

// ChunkDone returns a terminal CompletionChunk (Done: true).
func ChunkDone() llm.CompletionChunk {
	return llm.CompletionChunk{Done: true}
}

// --- Tool ---

// Tool is a minimal tools.Tool that returns canned data. Useful when
// the test only cares that the runner dispatches and the loop
// terminates correctly, not what the tool produces.
type Tool struct {
	Name        tools.ToolName
	Description string
	Result      string
	Err         error // non-nil ⇒ Execute returns this
}

// Definition advertises Name/Description verbatim with no parameter
// schema — scripted tools accept any arguments.
func (s Tool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: s.Name, Description: s.Description}
}

// Execute returns the canned Result (or Err when set), ignoring the
// call's arguments entirely.
func (s Tool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	return &tools.ToolResult{
		ToolCallID: call.ID,
		Success:    true,
		Data:       s.Result,
		ExecutedAt: time.Now(),
	}, nil
}

// --- Sink ---

// Sink records every runner event for assertion. Embeds runner.NopSink
// so future event additions don't force this fake to update — the
// recording is intentionally non-exhaustive.
//
// All accessors take the internal mutex; safe under -race.
type Sink struct {
	runner.NopSink
	mu        sync.Mutex
	contents  []runner.Content
	starts    []runner.ToolStarted
	completes []runner.ToolCompleted
	fails     []runner.ToolFailed
	convStart []runner.ConversationStarted
	convEnded []runner.ConversationEnded
	steers    []runner.SteerInjected
	compacts  []runner.CompactionApplied
}

// NewSink returns an empty recording sink.
func NewSink() *Sink { return &Sink{} }

// OnContent appends e to the contents slice under the mutex.
func (s *Sink) OnContent(e runner.Content) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contents = append(s.contents, e)
}

// OnToolStarted appends e to the starts slice under the mutex.
func (s *Sink) OnToolStarted(e runner.ToolStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, e)
}

// OnToolCompleted appends e to the completes slice under the mutex.
func (s *Sink) OnToolCompleted(e runner.ToolCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completes = append(s.completes, e)
}

// OnToolFailed appends e to the fails slice under the mutex.
func (s *Sink) OnToolFailed(e runner.ToolFailed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails = append(s.fails, e)
}

// OnConversationStarted appends e to the convStart slice under the mutex.
func (s *Sink) OnConversationStarted(e runner.ConversationStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convStart = append(s.convStart, e)
}

// OnConversationEnded appends e to the convEnded slice under the mutex.
func (s *Sink) OnConversationEnded(e runner.ConversationEnded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convEnded = append(s.convEnded, e)
}

// OnSteerInjected appends e to the steers slice under the mutex.
func (s *Sink) OnSteerInjected(e runner.SteerInjected) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steers = append(s.steers, e)
}

// OnCompactionApplied appends e to the compacts slice under the mutex.
func (s *Sink) OnCompactionApplied(e runner.CompactionApplied) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compacts = append(s.compacts, e)
}

// --- Sink read-side helpers ---

// ContentCount returns the number of OnContent calls received.
func (s *Sink) ContentCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.contents) }

// ContentText returns the concatenated content.Delta strings — the
// streamed assistant response as the runner saw it.
func (s *Sink) ContentText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sb []byte
	for _, c := range s.contents {
		sb = append(sb, c.Delta...)
	}
	return string(sb)
}

// ToolStartedCount returns the number of OnToolStarted events received.
func (s *Sink) ToolStartedCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.starts) }

// ToolCompletedCount returns the number of OnToolCompleted events received.
func (s *Sink) ToolCompletedCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.completes) }

// ToolFailedCount returns the number of OnToolFailed events received.
func (s *Sink) ToolFailedCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.fails) }

// FirstToolCompleted returns the first OnToolCompleted event, or false if
// none were received.
func (s *Sink) FirstToolCompleted() (runner.ToolCompleted, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.completes) == 0 {
		return runner.ToolCompleted{}, false
	}
	return s.completes[0], true
}

// FirstToolFailed returns the first OnToolFailed event, or false if none
// were received.
func (s *Sink) FirstToolFailed() (runner.ToolFailed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fails) == 0 {
		return runner.ToolFailed{}, false
	}
	return s.fails[0], true
}

// ConversationStartedCount returns the number of OnConversationStarted
// calls received.
func (s *Sink) ConversationStartedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.convStart)
}

// ConversationEndedCount returns the number of OnConversationEnded calls
// received (a Run ends exactly once, for any terminal reason).
func (s *Sink) ConversationEndedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.convEnded)
}

// ConversationEndedReasonCount returns how many ended events carried the
// given terminal reason — e.g. distinguish completed from error or
// max_iterations.
func (s *Sink) ConversationEndedReasonCount(reason runner.TerminalReason) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.convEnded {
		if e.Reason == reason {
			n++
		}
	}
	return n
}

// SteerCount returns the number of OnSteerInjected calls received.
func (s *Sink) SteerCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.steers) }

// FirstSteer returns the first OnSteerInjected event, or false if
// none were received.
func (s *Sink) FirstSteer() (runner.SteerInjected, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steers) == 0 {
		return runner.SteerInjected{}, false
	}
	return s.steers[0], true
}

// CompactionCount returns the number of OnCompactionApplied calls
// received.
func (s *Sink) CompactionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.compacts)
}
