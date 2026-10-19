package teasink

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	maxPendingToolOutputs = 128
	maxSealedToolOutputs  = 128
)

// The runner closes and joins each invocation's output publisher before its
// terminal event. The bounded sealed set additionally rejects recent late
// callbacks without retaining every execution identity for the Sink lifetime.

// toolOutputKey normally identifies an execution by task, depth, and execution
// ID. The remaining fields are explicit fallback identity for legacy callers
// that do not provide an execution ID.
type toolOutputKey struct {
	taskID            string
	depth             int
	executionID       string
	toolID            string
	toolName          string
	parentExecutionID string
	parentToolID      string
	sequence          int
}

func toolOutputKeyFor(
	taskID string,
	depth int,
	executionID string,
	toolID string,
	toolName string,
	parentExecutionID string,
	parentToolID string,
	sequence int,
) toolOutputKey {
	key := toolOutputKey{taskID: taskID, depth: depth, executionID: executionID}
	if executionID != "" {
		return key
	}
	key.toolID = toolID
	key.toolName = toolName
	key.parentExecutionID = parentExecutionID
	key.parentToolID = parentToolID
	key.sequence = sequence
	return key
}

// OnToolOutput retains only the latest cumulative preview for an execution.
// It never waits for the sink lock or pump: under contention or when the
// bounded pending set is full, this best-effort live update is dropped.
func (s *Sink) OnToolOutput(_ context.Context, event runner.ToolOutputProgress) {
	if s.stopped() {
		return
	}
	text := capToolOutputText(event.Text)
	key := toolOutputKeyFor(
		string(event.TaskID), event.Depth, event.ExecutionID, event.ToolID, event.ToolName,
		event.ParentExecutionID, event.ParentToolID, event.Sequence,
	)
	msg := ToolOutputMsg{
		TaskID:            string(event.TaskID),
		Depth:             event.Depth,
		ExecutionID:       event.ExecutionID,
		ToolID:            event.ToolID,
		ToolName:          event.ToolName,
		ParentExecutionID: event.ParentExecutionID,
		ParentToolID:      event.ParentToolID,
		Sequence:          event.Sequence,
		Text:              text,
	}

	if !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	if s.stopped() {
		return
	}
	if _, sealed := s.sealedToolOutputs[key]; sealed {
		return
	}
	if _, exists := s.pendingToolOutputs[key]; !exists {
		if len(s.pendingToolOutputs) >= maxPendingToolOutputs {
			return
		}
		s.toolOutputOrder = append(s.toolOutputOrder, key)
	}
	s.pendingToolOutputs[key] = msg
	if s.timer == nil {
		s.timer = time.AfterFunc(coalesceWindow, s.flush)
	}
}

func capToolOutputText(text string) string {
	if len(text) > tools.MaxToolOutputPreviewBytes {
		text = text[:tools.MaxToolOutputPreviewBytes]
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "\uFFFD")
	}
	if len(text) <= tools.MaxToolOutputPreviewBytes {
		return text
	}
	text = text[:tools.MaxToolOutputPreviewBytes]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func (s *Sink) startToolOutput(key toolOutputKey, msg tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
	s.unsealToolOutputLocked(key)
	s.dispatch(msg)
}

func (s *Sink) settleToolOutput(key toolOutputKey, msg tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
	s.sealToolOutputLocked(key)
	s.dispatch(msg)
}

func (s *Sink) sealToolOutputLocked(key toolOutputKey) {
	if _, exists := s.sealedToolOutputs[key]; exists {
		return
	}
	if len(s.sealedToolOrder) == maxSealedToolOutputs {
		oldest := s.sealedToolOrder[0]
		delete(s.sealedToolOutputs, oldest)
		copy(s.sealedToolOrder, s.sealedToolOrder[1:])
		s.sealedToolOrder = s.sealedToolOrder[:len(s.sealedToolOrder)-1]
	}
	s.sealedToolOutputs[key] = struct{}{}
	s.sealedToolOrder = append(s.sealedToolOrder, key)
}

func (s *Sink) unsealToolOutputLocked(key toolOutputKey) {
	if _, exists := s.sealedToolOutputs[key]; !exists {
		return
	}
	delete(s.sealedToolOutputs, key)
	for index, sealed := range s.sealedToolOrder {
		if sealed != key {
			continue
		}
		copy(s.sealedToolOrder[index:], s.sealedToolOrder[index+1:])
		s.sealedToolOrder = s.sealedToolOrder[:len(s.sealedToolOrder)-1]
		return
	}
}

func (s *Sink) stopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}
