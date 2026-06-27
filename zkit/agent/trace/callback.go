package trace

import (
	"context"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// Sink adapts runner events into trace Events. Export errors are retained and
// can be inspected with Err; runner.EventSink methods cannot return them.
type Sink struct {
	Exporter Exporter
	mu       sync.Mutex
	err      error
}

// Err returns the first exporter error observed by Sink.
func (s *Sink) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Sink) emit(kind Kind, taskID taskscope.ID, depth int, name string, payload any) {
	if s == nil || s.Exporter == nil {
		return
	}
	e := Event{Time: time.Now(), Kind: kind, TaskID: taskID, Depth: depth, Name: name, Payload: payload}
	if err := s.Exporter.Export(context.Background(), e); err != nil {
		s.mu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.mu.Unlock()
	}
}

// OnContent exports a content event.
func (s *Sink) OnContent(e runner.Content) { s.emit(KindContent, e.TaskID, e.Depth, "", e) }

// OnThinking exports a thinking event.
func (s *Sink) OnThinking(e runner.Thinking) { s.emit(KindThinking, e.TaskID, e.Depth, "", e) }

// OnToolStarted exports a tool-started event.
func (s *Sink) OnToolStarted(e runner.ToolStarted) {
	s.emit(KindToolStarted, e.TaskID, e.Depth, e.ToolName, e)
}

// OnToolCompleted exports a tool-completed event.
func (s *Sink) OnToolCompleted(e runner.ToolCompleted) {
	s.emit(KindToolCompleted, e.TaskID, e.Depth, e.ToolName, e)
}

// OnToolFailed exports a tool-failed event.
func (s *Sink) OnToolFailed(e runner.ToolFailed) {
	s.emit(KindToolFailed, e.TaskID, e.Depth, e.ToolName, e)
}

// OnConversationStarted exports a conversation-started event.
func (s *Sink) OnConversationStarted(e runner.ConversationStarted) {
	s.emit(KindConversationStarted, e.TaskID, e.Depth, e.AgentName, e)
}

// OnConversationEnded exports a conversation-ended event.
func (s *Sink) OnConversationEnded(e runner.ConversationEnded) {
	s.emit(KindConversationEnded, e.TaskID, e.Depth, "", e)
}

// OnIterationCompleted exports an iteration-completed event.
func (s *Sink) OnIterationCompleted(e runner.IterationCompleted) {
	s.emit(KindIterationCompleted, e.TaskID, e.Depth, "", e)
}

// OnSteerInjected exports a steer-injected event.
func (s *Sink) OnSteerInjected(e runner.SteerInjected) {
	s.emit(KindSteerInjected, e.TaskID, e.Depth, "", e)
}

// OnCompactionApplied exports a compaction-applied event.
func (s *Sink) OnCompactionApplied(e runner.CompactionApplied) {
	s.emit(KindCompactionApplied, e.TaskID, e.Depth, e.Engine, e)
}
