package transcript

import (
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

var (
	// ErrUnsupportedEvent means a caller attempted a mutation outside the canonical
	// transcript event vocabulary.
	ErrUnsupportedEvent = errors.New("unsupported transcript event")
	// ErrInvalidEvent means a recognized event is missing required semantics.
	ErrInvalidEvent = errors.New("invalid transcript event")
)

// Reducer is the only runtime mutation boundary for a canonical thread.
type Reducer struct{ builder *Builder }

// NewReducer creates an empty transcript reducer.
func NewReducer() *Reducer { return &Reducer{builder: NewBuilder()} }

// NewReducerFrom creates a reducer over an existing validated thread.
func NewReducerFrom(thread Thread) *Reducer { return &Reducer{builder: NewBuilderFrom(thread)} }

// Thread returns the reducer's current canonical thread.
func (r *Reducer) Thread() Thread { return r.builder.Thread() }

// Change describes one accepted semantic event and its canonical effects.
type Change struct {
	BeforeRevision uint64
	AfterRevision  uint64
	PrimaryEntryID string
	Persistence    Persistence
	Records        []Record
}

// Changed reports whether the event advanced canonical state.
func (c Change) Changed() bool { return c.AfterRevision != c.BeforeRevision }

// Clear removes every canonical entry.
type Clear struct{}

// UserSubmitted appends a submitted user message.
type UserSubmitted struct {
	Text        string
	Attachments []Attachment
}

// QueuedUserAdded appends a queued user message.
type QueuedUserAdded struct{ Text string }

// QueuedUserInjected marks a queued message delivered to the runner.
type QueuedUserInjected struct {
	EntryID string
	Text    string
}

// NoticeAdded appends a visible notice, optionally owned by a turn.
type NoticeAdded struct {
	TurnID string
	Text   string
}

// TurnStarted opens an assistant turn.
type TurnStarted struct{ TurnID string }

// AssistantDelta appends streamed assistant content.
type AssistantDelta struct {
	TurnID string
	Delta  string
}

// ReasoningDelta appends streamed reasoning content.
type ReasoningDelta struct {
	TurnID string
	Delta  string
}

// TurnFinished closes streamed entries for a turn.
type TurnFinished struct{ TurnID string }

// SkillLoaded records a skill loaded during a turn.
type SkillLoaded struct {
	TurnID string
	Name   string
}

// ToolStarted records a tool invocation.
type ToolStarted struct {
	TurnID       string
	ToolID       string
	ParentToolID string
	Name         string
	Argument     string
	Sequence     int
}

// ToolFinished records a tool terminal outcome.
type ToolFinished struct {
	ToolID      string
	Effect      string
	FailureKind string
	DurationMS  int64
	Failed      bool
}

// DiffAdded records a visible file mutation.
type DiffAdded struct {
	TurnID string
	Path   string
	Diff   string
}

// PlanUpdated records the latest canonical plan.
type PlanUpdated struct {
	TurnID string
	Plan   code.Plan
}

// SubagentReserved records a requested child before its task ID exists.
type SubagentReserved struct {
	SpawnToolID string
	AgentName   string
	Prompt      string
}

// SubagentStarted binds and starts a child task.
type SubagentStarted struct {
	TurnID      string
	SpawnToolID string
	AgentName   string
	Prompt      string
}

// SubagentFinished marks a child task terminal.
type SubagentFinished struct{ TurnID string }

// SubagentSpawnFailed marks a spawn failure before the child starts.
type SubagentSpawnFailed struct {
	SpawnToolID string
	Detail      string
}

// Apply reduces one semantic event into canonical state. Unknown event types are
// rejected explicitly and never mutate the thread.
func (r *Reducer) Apply(event any) (Change, error) {
	before := r.builder.Thread().Revision()
	change := Change{BeforeRevision: before, AfterRevision: before}

	if err := validateEvent(event); err != nil {
		return change, err
	}
	change.Persistence = persistenceForEvent(event)
	switch event := event.(type) {
	case Clear:
		r.builder.Clear()
		change.AfterRevision = r.builder.Thread().Revision()
		return change, nil
	case UserSubmitted:
		r.builder.AddUserWithAttachments(event.Text, event.Attachments)
	case QueuedUserAdded:
		change.PrimaryEntryID = r.builder.AddQueuedUser(event.Text)
	case QueuedUserInjected:
		r.builder.InjectQueuedUser(event.EntryID, event.Text)
	case NoticeAdded:
		r.builder.AddTurnNotice(event.TurnID, event.Text)
	case TurnStarted:
		r.builder.StartTurn(event.TurnID, r.builder.SubagentEntryID(event.TurnID))
	case AssistantDelta:
		r.builder.AppendAssistant(event.TurnID, r.builder.SubagentEntryID(event.TurnID), event.Delta)
	case ReasoningDelta:
		r.builder.AppendReasoning(event.TurnID, r.builder.SubagentEntryID(event.TurnID), event.Delta)
	case TurnFinished:
		r.builder.FinishTurn(event.TurnID)
	case SkillLoaded:
		r.builder.AddSkill(event.TurnID, r.builder.SubagentEntryID(event.TurnID), event.Name)
	case ToolStarted:
		parentID := r.builder.SubagentEntryID(event.TurnID)
		if event.ParentToolID != "" {
			parentID = r.builder.ToolEntryID(event.ParentToolID)
		}
		change.PrimaryEntryID = r.builder.StartTool(
			event.TurnID, parentID, event.ToolID, event.ParentToolID,
			event.Name, event.Argument, event.Sequence,
		)
	case ToolFinished:
		r.builder.FinishTool(event.ToolID, event.Effect, event.FailureKind, event.DurationMS, event.Failed)
	case DiffAdded:
		r.builder.AddDiff(event.TurnID, r.builder.SubagentEntryID(event.TurnID), event.Path, event.Diff)
	case PlanUpdated:
		r.builder.SetPlan(event.TurnID, event.Plan)
	case SubagentReserved:
		change.PrimaryEntryID = r.builder.ReserveSubagent(event.SpawnToolID, event.AgentName, event.Prompt)
	case SubagentStarted:
		change.PrimaryEntryID = r.builder.StartSubagent(event.TurnID, event.SpawnToolID, event.AgentName, event.Prompt)
	case SubagentFinished:
		r.builder.FinishSubagent(event.TurnID)
	case SubagentSpawnFailed:
		r.builder.FailSubagent(event.SpawnToolID, event.Detail)
	default:
		return change, fmt.Errorf("%w: %T", ErrUnsupportedEvent, event)
	}

	after := r.builder.Thread()
	change.AfterRevision = after.Revision()
	if !change.Changed() {
		return change, nil
	}
	records, err := after.RecordsSince(before)
	if err != nil {
		return Change{}, fmt.Errorf("reduce transcript event %T: %w", event, err)
	}
	change.Records = records
	return change, nil
}

func persistenceForEvent(event any) Persistence {
	switch event.(type) {
	case AssistantDelta, ReasoningDelta:
		return Persistences.PERSISTENCEDEBOUNCED
	case UserSubmitted, QueuedUserAdded, QueuedUserInjected, NoticeAdded,
		TurnStarted, TurnFinished, SkillLoaded, ToolStarted, ToolFinished,
		DiffAdded, PlanUpdated, SubagentReserved, SubagentStarted,
		SubagentFinished, SubagentSpawnFailed:
		return Persistences.PERSISTENCEIMMEDIATE
	case Clear:
		return Persistences.PERSISTENCENONE
	default:
		return Persistences.PERSISTENCENONE
	}
}

func validateEvent(event any) error {
	invalid := func(detail string) error { return fmt.Errorf("%w: %s", ErrInvalidEvent, detail) }
	switch event := event.(type) {
	case Clear:
		return nil
	case UserSubmitted:
		if event.Text == "" && len(event.Attachments) == 0 {
			return invalid("user text and attachments are empty")
		}
		for _, attachment := range event.Attachments {
			if attachment.Name == "" {
				return invalid("attachment name is empty")
			}
			if attachment.Size < 0 {
				return invalid("attachment size is negative")
			}
		}
	case QueuedUserAdded:
		if event.Text == "" {
			return invalid("queued user text is empty")
		}
	case QueuedUserInjected:
		if event.EntryID == "" || event.Text == "" {
			return invalid("queued user identity or text is empty")
		}
	case NoticeAdded:
		if event.Text == "" {
			return invalid("notice text is empty")
		}
	case TurnStarted:
		if event.TurnID == "" {
			return invalid("turn ID is empty")
		}
	case TurnFinished:
		if event.TurnID == "" {
			return invalid("turn ID is empty")
		}
	case AssistantDelta:
		if event.TurnID == "" {
			return invalid("assistant turn ID is empty")
		}
	case ReasoningDelta:
		if event.TurnID == "" {
			return invalid("reasoning turn ID is empty")
		}
	case SkillLoaded:
		if event.TurnID == "" || event.Name == "" {
			return invalid("skill turn ID or name is empty")
		}
	case ToolStarted:
		if event.TurnID == "" || event.ToolID == "" || event.Name == "" {
			return invalid("tool turn ID, call ID, or name is empty")
		}
	case ToolFinished:
		if event.ToolID == "" || event.DurationMS < 0 {
			return invalid("tool call ID is empty or duration is negative")
		}
	case DiffAdded:
		if event.TurnID == "" || event.Path == "" || event.Diff == "" {
			return invalid("diff turn ID, path, or body is empty")
		}
	case PlanUpdated:
		if event.TurnID == "" || len(event.Plan.Steps) == 0 {
			return invalid("plan turn ID is empty or plan has no steps")
		}
	case SubagentReserved:
		if event.AgentName == "" {
			return invalid("subagent name is empty")
		}
	case SubagentStarted:
		if event.TurnID == "" || event.AgentName == "" {
			return invalid("subagent turn ID or name is empty")
		}
	case SubagentFinished:
		if event.TurnID == "" {
			return invalid("subagent turn ID is empty")
		}
	case SubagentSpawnFailed:
		if event.SpawnToolID == "" {
			return invalid("subagent spawn tool ID is empty")
		}
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedEvent, event)
	}
	return nil
}
