package trace

import (
	"context"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/workflow"
)

// WorkflowSink adapts workflow events into trace Events. Export errors are
// retained and can be inspected with Err.
type WorkflowSink struct {
	Exporter Exporter
	mu       sync.Mutex
	err      error
}

// Err returns the first exporter error observed by WorkflowSink.
func (s *WorkflowSink) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *WorkflowSink) emit(kind Kind, name string, payload any) {
	if s == nil || s.Exporter == nil {
		return
	}
	if err := s.Exporter.Export(context.Background(), Event{Time: time.Now(), Kind: kind, Name: name, Payload: payload}); err != nil {
		s.mu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.mu.Unlock()
	}
}

// OnWorkflowStarted exports a workflow-started event.
func (s *WorkflowSink) OnWorkflowStarted(e workflow.Started) { s.emit(KindWorkflowStarted, "", e) }

// OnWorkflowNodeStarted exports a workflow-node-started event.
func (s *WorkflowSink) OnWorkflowNodeStarted(e workflow.NodeStarted) {
	s.emit(KindWorkflowNodeStarted, e.Node, e)
}

// OnWorkflowNodeCompleted exports a workflow-node-completed event.
func (s *WorkflowSink) OnWorkflowNodeCompleted(e workflow.NodeCompleted) {
	s.emit(KindWorkflowNodeDone, e.Node, e)
}

// OnWorkflowNodeFailed exports a workflow-node-failed event.
func (s *WorkflowSink) OnWorkflowNodeFailed(e workflow.NodeFailed) {
	s.emit(KindWorkflowNodeFailed, e.Node, e)
}

// OnWorkflowCompleted exports a workflow-completed event.
func (s *WorkflowSink) OnWorkflowCompleted(e workflow.Completed) {
	s.emit(KindWorkflowCompleted, "", e)
}

// OnWorkflowFailed exports a workflow-failed event.
func (s *WorkflowSink) OnWorkflowFailed(e workflow.Failed) { s.emit(KindWorkflowFailed, "", e) }
