package runner

//go:generate go tool goenums -f replay_occurrence_kind_enum.go

// replayOccurrenceKind separates model observations from inspection-only data.
// Message is deliberately the valid zero value for legacy omitted kinds.
type replayOccurrenceKind int

const (
	message       replayOccurrenceKind = iota // message
	toolExecution                             // tool_execution
)
