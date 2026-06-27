package workflow

import "time"

// Started fires when a workflow invocation begins.
type Started struct {
	Input any
}

// NodeStarted fires immediately before a node runs.
type NodeStarted struct {
	Node  string
	Input any
}

// NodeCompleted fires after a node returns successfully.
type NodeCompleted struct {
	Node     string
	Output   any
	Duration time.Duration
}

// NodeFailed fires after a node returns an error.
type NodeFailed struct {
	Node     string
	Error    error
	Duration time.Duration
}

// Completed fires when a workflow reaches End.
type Completed struct {
	Output   any
	State    State
	Duration time.Duration
}

// Failed fires when workflow execution terminates with an error.
type Failed struct {
	Error    error
	State    State
	Duration time.Duration
}

// EventSink observes workflow execution.
type EventSink interface {
	OnWorkflowStarted(Started)
	OnWorkflowNodeStarted(NodeStarted)
	OnWorkflowNodeCompleted(NodeCompleted)
	OnWorkflowNodeFailed(NodeFailed)
	OnWorkflowCompleted(Completed)
	OnWorkflowFailed(Failed)
}

// NopSink satisfies EventSink with no-op methods.
type NopSink struct{}

// OnWorkflowStarted discards the event.
func (NopSink) OnWorkflowStarted(Started) {}

// OnWorkflowNodeStarted discards the event.
func (NopSink) OnWorkflowNodeStarted(NodeStarted) {}

// OnWorkflowNodeCompleted discards the event.
func (NopSink) OnWorkflowNodeCompleted(NodeCompleted) {}

// OnWorkflowNodeFailed discards the event.
func (NopSink) OnWorkflowNodeFailed(NodeFailed) {}

// OnWorkflowCompleted discards the event.
func (NopSink) OnWorkflowCompleted(Completed) {}

// OnWorkflowFailed discards the event.
func (NopSink) OnWorkflowFailed(Failed) {}
