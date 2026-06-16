package events

import (
	"time"
)

// EventType identifies the kind of event.
type EventType string

const (
	SessionEnded   EventType = "session.ended"
	TaskFinding    EventType = "task.finding"
	MemoryStored   EventType = "memory.stored"
	ToolProposed   EventType = "tool.proposed"
	PromptProposed EventType = "prompt.proposed"
)

// Event is an occurrence in the system that handlers can react to.
type Event struct {
	Type    EventType
	Payload any
	Time    time.Time
}

// SessionEndedPayload carries data for a SessionEnded event.
type SessionEndedPayload struct {
	SessionID  string
	PersonName string
	Messages   []Message
}

// Message is a minimal representation of a chat message for event handlers.
type Message struct {
	Role    string
	Content string
}

// TaskFindingPayload carries data for a TaskFinding event.
type TaskFindingPayload struct {
	TaskID     string
	Finding    string
	PersonName string
}

// ToolProposedPayload carries data for a ToolProposed event.
type ToolProposedPayload struct {
	ToolName    string
	Description string
	MCPURL      string
	Rationale   string
}

// PromptProposedPayload carries data for a PromptProposed event.
type PromptProposedPayload struct {
	CurrentPromptID string
	Rationale       string
}
