// Package transcript defines the durable, renderer-independent human thread.
package transcript

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// ToolState describes a durable tool-call outcome.
type ToolState string

const (
	// ToolRunning means the call had not reached a terminal event when captured.
	ToolRunning ToolState = "running"
	// ToolSucceeded means the call completed successfully.
	ToolSucceeded ToolState = "succeeded"
	// ToolFailed means the call completed unsuccessfully.
	ToolFailed ToolState = "failed"
	// ToolInterrupted means process termination ended the call without a terminal event.
	ToolInterrupted ToolState = "interrupted"
)

// SubagentState describes a durable sub-agent lifecycle state.
type SubagentState string

const (
	// SubagentPending means spawn was requested but the child had not started.
	SubagentPending SubagentState = "pending"
	// SubagentRunning means the child started and had not reached a terminal event.
	SubagentRunning SubagentState = "running"
	// SubagentCompleted means the child reached a terminal event.
	SubagentCompleted SubagentState = "completed"
	// SubagentFailed means spawning the child failed.
	SubagentFailed SubagentState = "failed"
	// SubagentInterrupted means process termination ended a running child.
	SubagentInterrupted SubagentState = "interrupted"
)

// Attachment describes human-visible submitted media without provider payload bytes.
type Attachment struct {
	Name     string `json:"name"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

// Thread is the canonical ordered human-visible conversation.
type Thread struct {
	revision uint64
	entries  []Entry
}

// Revision returns the latest semantic mutation revision.
func (t Thread) Revision() uint64 { return t.revision }

// Entries returns a copy of the ordered thread entries.
func (t Thread) Entries() []Entry {
	entries := make([]Entry, len(t.entries))
	for i, entry := range t.entries {
		entries[i] = cloneEntry(entry)
	}
	return entries
}

// IsEmpty reports whether the thread contains no durable entries.
func (t Thread) IsEmpty() bool { return len(t.entries) == 0 }

// MessageCount returns the number of top-level human and assistant messages.
func (t Thread) MessageCount() int {
	count := 0
	for _, entry := range t.entries {
		if entry.ParentID != "" {
			continue
		}
		if entry.Kind == EntryKinds.ENTRYUSERMESSAGE || entry.Kind == EntryKinds.ENTRYASSISTANTMESSAGE {
			count++
		}
	}
	return count
}

// Entry is one semantic event in the durable thread.
func cloneEntry(entry Entry) Entry {
	entry.Payload = clonePayload(entry.Payload)
	return entry
}

func clonePayload(payload Payload) Payload {
	payload.Plan = clonePlan(payload.Plan)
	payload.Skills = append([]string(nil), payload.Skills...)
	payload.Attachments = append([]Attachment(nil), payload.Attachments...)
	return payload
}

func clonePlan(plan code.Plan) code.Plan {
	plan.Steps = append([]code.PlanStep(nil), plan.Steps...)
	return plan
}

type Entry struct {
	ID       string
	ParentID string
	TurnID   string
	Kind     EntryKind
	Revision uint64
	Payload  Payload `json:"payload"`
}

// Payload carries kind-specific durable facts. Fields unrelated to Kind remain zero.
type Payload struct {
	Text        string `json:"text,omitempty"`
	Injected    bool   `json:"injected,omitempty"`
	Complete    bool   `json:"complete,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`

	ToolID       string    `json:"tool_id,omitempty"`
	ParentToolID string    `json:"parent_tool_id,omitempty"`
	ToolName     string    `json:"tool_name,omitempty"`
	Argument     string    `json:"argument,omitempty"`
	Effect       string    `json:"effect,omitempty"`
	ToolState    ToolState `json:"tool_state,omitempty"`
	FailureKind  string    `json:"failure_kind,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	Sequence     int       `json:"sequence,omitempty"`

	Path        string       `json:"path,omitempty"`
	Diff        string       `json:"diff,omitempty"`
	Plan        code.Plan    `json:"plan,omitzero"`
	Skills      []string     `json:"skills,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`

	AgentName   string        `json:"agent_name,omitempty"`
	Prompt      string        `json:"prompt,omitempty"`
	SpawnToolID string        `json:"spawn_tool_id,omitempty"`
	Subagent    SubagentState `json:"subagent,omitempty"`
}

// Builder mutates one Thread while maintaining stable semantic identities.
type Builder struct {
	thread       Thread
	nextID       int
	turnResponse map[string]string
	turnReason   map[string]string
	turnSkills   map[string]string
	tools        map[string]string
	subagents    map[string]string
	spawnAgents  map[string]string
	currentPlan  string
}

// NewBuilder creates an empty canonical thread builder.
func NewBuilder() *Builder {
	return &Builder{
		turnResponse: make(map[string]string),
		turnReason:   make(map[string]string),
		turnSkills:   make(map[string]string),
		tools:        make(map[string]string),
		subagents:    make(map[string]string),
		spawnAgents:  make(map[string]string),
	}
}

// NewBuilderFrom creates a builder over an existing validated thread.
func NewBuilderFrom(thread Thread) *Builder {
	builder := NewBuilder()
	builder.thread = Thread{revision: thread.Revision(), entries: thread.Entries()}
	for _, entry := range builder.thread.entries {
		if number, ok := generatedEntryNumber(entry.ID); ok && number > builder.nextID {
			builder.nextID = number
		}
		switch entry.Kind {
		case EntryKinds.ENTRYASSISTANTMESSAGE:
			if !entry.Payload.Complete && !entry.Payload.Interrupted && entry.TurnID != "" {
				builder.turnResponse[entry.TurnID] = entry.ID
			}
		case EntryKinds.ENTRYREASONING:
			if !entry.Payload.Complete && !entry.Payload.Interrupted && entry.TurnID != "" {
				builder.turnReason[entry.TurnID] = entry.ID
			}
		case EntryKinds.ENTRYSKILLS:
			if entry.TurnID != "" {
				builder.turnSkills[entry.TurnID] = entry.ID
			}
		case EntryKinds.ENTRYTOOLCALL:
			builder.tools[entry.Payload.ToolID] = entry.ID
		case EntryKinds.ENTRYSUBAGENT:
			if entry.TurnID != "" {
				builder.subagents[entry.TurnID] = entry.ID
			}
			if entry.Payload.SpawnToolID != "" {
				builder.spawnAgents[entry.Payload.SpawnToolID] = entry.ID
			}
		case EntryKinds.ENTRYPLAN:
			builder.currentPlan = entry.ID
		}
	}
	return builder
}

func generatedEntryNumber(id string) (int, bool) {
	number, err := strconv.Atoi(strings.TrimPrefix(id, "e"))
	return number, err == nil && strings.HasPrefix(id, "e") && number > 0
}

// Thread returns a copy of the builder's current canonical thread.
func (b *Builder) Thread() Thread {
	return Thread{revision: b.thread.revision, entries: b.thread.Entries()}
}

// Clear removes every canonical transcript entry and active index.
func (b *Builder) Clear() { *b = *NewBuilder() }

func (b *Builder) append(kind EntryKind, parentID, turnID string, payload Payload) string {
	b.nextID++
	b.thread.revision++
	id := fmt.Sprintf("e%d", b.nextID)
	b.thread.entries = append(b.thread.entries, Entry{ID: id, ParentID: parentID, TurnID: turnID, Kind: kind, Revision: b.thread.revision, Payload: payload})
	return id
}

func (b *Builder) update(id string, update func(*Entry)) {
	for i := range b.thread.entries {
		if b.thread.entries[i].ID == id {
			b.thread.revision++
			update(&b.thread.entries[i])
			b.thread.entries[i].Revision = b.thread.revision
			return
		}
	}
}

// AddUser appends a submitted top-level user message.
func (b *Builder) AddUser(text string) {
	b.AddUserWithAttachments(text, nil)
}

// AddUserWithAttachments appends submitted text and durable attachment metadata.
func (b *Builder) AddUserWithAttachments(text string, attachments []Attachment) {
	b.append(EntryKinds.ENTRYUSERMESSAGE, "", "", Payload{
		Text: text, Attachments: append([]Attachment(nil), attachments...),
	})
}

// AddQueuedUser appends a queued top-level user message and returns its identity.
func (b *Builder) AddQueuedUser(text string) string {
	return b.append(EntryKinds.ENTRYQUEUEDUSER, "", "", Payload{Text: text})
}

// InjectQueuedUser turns a queued entry into the submitted text delivered to the runner.
func (b *Builder) InjectQueuedUser(id, text string) {
	b.update(id, func(entry *Entry) {
		entry.Payload.Text = text
		entry.Payload.Injected = true
	})
}

// AddNotice appends a durable human-visible notice.
func (b *Builder) AddNotice(parentID, text string) {
	b.append(EntryKinds.ENTRYNOTICE, parentID, "", Payload{Text: text})
}

// AddTurnNotice appends a notice owned by turnID.
func (b *Builder) AddTurnNotice(turnID, text string) {
	b.AddNotice(b.SubagentEntryID(turnID), text)
}

// StartTurn creates the assistant entry for turnID.
func (b *Builder) StartTurn(turnID, parentID string) {
	if _, ok := b.turnResponse[turnID]; ok {
		return
	}
	b.turnResponse[turnID] = b.append(EntryKinds.ENTRYASSISTANTMESSAGE, parentID, turnID, Payload{})
}

// AppendAssistant accumulates streamed assistant text for turnID.
func (b *Builder) AppendAssistant(turnID, parentID, delta string) {
	if delta == "" {
		return
	}
	b.StartTurn(turnID, parentID)
	b.update(b.turnResponse[turnID], func(entry *Entry) { entry.Payload.Text += delta })
}

// AppendReasoning accumulates streamed reasoning for turnID.
func (b *Builder) AppendReasoning(turnID, parentID, delta string) {
	if delta == "" {
		return
	}
	b.StartTurn(turnID, parentID)
	id := b.turnReason[turnID]
	if id == "" {
		id = b.append(EntryKinds.ENTRYREASONING, parentID, turnID, Payload{})
		b.turnReason[turnID] = id
	}
	b.update(id, func(entry *Entry) { entry.Payload.Text += delta })
}

// FinishTurn marks streamed assistant and reasoning entries complete.
func (b *Builder) FinishTurn(turnID string) {
	for _, id := range []string{b.turnResponse[turnID], b.turnReason[turnID]} {
		if id != "" {
			b.update(id, func(entry *Entry) { entry.Payload.Complete = true })
		}
	}
	delete(b.turnResponse, turnID)
	delete(b.turnReason, turnID)
	delete(b.turnSkills, turnID)
}

// AddSkill records one skill loaded during turnID.
func (b *Builder) AddSkill(turnID, parentID, name string) {
	id := b.turnSkills[turnID]
	if id == "" {
		id = b.append(EntryKinds.ENTRYSKILLS, parentID, turnID, Payload{})
		b.turnSkills[turnID] = id
	}
	b.update(id, func(entry *Entry) {
		for _, existing := range entry.Payload.Skills {
			if existing == name {
				return
			}
		}
		entry.Payload.Skills = append(entry.Payload.Skills, name)
	})
}

// StartTool records one tool call and returns its entry identity.
func (b *Builder) StartTool(turnID, parentID, toolID, parentToolID, name, argument string, sequence int) string {
	if id := b.tools[toolID]; id != "" {
		return id
	}
	id := b.append(EntryKinds.ENTRYTOOLCALL, parentID, turnID, Payload{
		ToolID: toolID, ParentToolID: parentToolID, ToolName: name,
		Argument: argument, ToolState: ToolRunning, Sequence: sequence,
	})
	b.tools[toolID] = id
	return id
}

// ToolEntryID returns the semantic entry for toolID.
func (b *Builder) ToolEntryID(toolID string) string { return b.tools[toolID] }

// FinishTool records one tool call's durable terminal facts.
func (b *Builder) FinishTool(toolID, effect, failureKind string, durationMS int64, failed bool) {
	id := b.tools[toolID]
	if id == "" {
		return
	}
	b.update(id, func(entry *Entry) {
		entry.Payload.ToolState = ToolSucceeded
		if failed {
			entry.Payload.ToolState = ToolFailed
		}
		entry.Payload.Effect = effect
		entry.Payload.FailureKind = failureKind
		entry.Payload.DurationMS = durationMS
	})
}

// AddDiff appends a durable file mutation.
func (b *Builder) AddDiff(turnID, parentID, path, diff string) {
	b.append(EntryKinds.ENTRYDIFF, parentID, turnID, Payload{Path: path, Diff: diff})
}

// SetPlan records the latest plan state without persisting presentation state.
func (b *Builder) SetPlan(turnID string, plan code.Plan) {
	if b.currentPlan == "" {
		b.currentPlan = b.append(EntryKinds.ENTRYPLAN, "", turnID, Payload{Plan: clonePlan(plan)})
		return
	}
	b.update(b.currentPlan, func(entry *Entry) {
		entry.TurnID = turnID
		entry.Payload.Plan = clonePlan(plan)
	})
}

// ReserveSubagent records a requested child task before its task identity exists.
func (b *Builder) ReserveSubagent(spawnToolID, agentName, prompt string) string {
	if id := b.spawnAgents[spawnToolID]; id != "" {
		return id
	}
	id := b.append(EntryKinds.ENTRYSUBAGENT, "", "", Payload{
		AgentName: agentName, Prompt: prompt, SpawnToolID: spawnToolID, Subagent: SubagentPending,
	})
	if spawnToolID != "" {
		b.spawnAgents[spawnToolID] = id
	}
	return id
}

// StartSubagent binds a reserved child task or appends a running child entry.
func (b *Builder) StartSubagent(turnID, spawnToolID, agentName, prompt string) string {
	id := b.spawnAgents[spawnToolID]
	if id == "" {
		id = b.ReserveSubagent(spawnToolID, agentName, prompt)
	}
	b.update(id, func(entry *Entry) {
		entry.TurnID = turnID
		entry.Payload.AgentName = agentName
		entry.Payload.Prompt = prompt
		entry.Payload.Subagent = SubagentRunning
	})
	b.subagents[turnID] = id
	return id
}

// FinishSubagent marks a child task terminal.
func (b *Builder) FinishSubagent(turnID string) {
	id := b.subagents[turnID]
	b.update(id, func(entry *Entry) { entry.Payload.Subagent = SubagentCompleted })
	delete(b.subagents, turnID)
}

// FailSubagent marks a reserved spawn terminal before the child starts.
func (b *Builder) FailSubagent(spawnToolID, detail string) {
	id := b.spawnAgents[spawnToolID]
	b.update(id, func(entry *Entry) {
		entry.Payload.Subagent = SubagentFailed
		if detail != "" {
			entry.Payload.Text = detail
		}
	})
}

// SubagentEntryID returns the semantic parent entry for a child task.
func (b *Builder) SubagentEntryID(turnID string) string { return b.subagents[turnID] }

// RecoverInterrupted advances nonterminal lifecycle entries to explicit interrupted states.
// It returns the recovered thread and whether any durable transition was required.
func (t Thread) RecoverInterrupted() (Thread, bool) {
	builder := NewBuilderFrom(t)
	changed := false
	for i := range builder.thread.entries {
		entry := builder.thread.entries[i]
		switch entry.Kind {
		case EntryKinds.ENTRYASSISTANTMESSAGE, EntryKinds.ENTRYREASONING:
			if !entry.Payload.Complete && !entry.Payload.Interrupted {
				builder.update(entry.ID, func(current *Entry) { current.Payload.Interrupted = true })
				changed = true
			}
		case EntryKinds.ENTRYTOOLCALL:
			if entry.Payload.ToolState == ToolRunning {
				builder.update(entry.ID, func(current *Entry) { current.Payload.ToolState = ToolInterrupted })
				changed = true
			}
		case EntryKinds.ENTRYSUBAGENT:
			if entry.Payload.Subagent == SubagentPending || entry.Payload.Subagent == SubagentRunning {
				builder.update(entry.ID, func(current *Entry) { current.Payload.Subagent = SubagentInterrupted })
				changed = true
			}
		}
	}
	return builder.Thread(), changed
}

// Validate checks stable identity, revision consistency, parent ordering, and kind payloads.
func (t Thread) Validate() error {
	if len(t.entries) == 0 {
		if t.revision != 0 {
			return fmt.Errorf("validate transcript: empty thread has revision %d", t.revision)
		}
		return nil
	}
	seen := make(map[string]EntryKind, len(t.entries))
	seenToolIDs := make(map[string]struct{})
	seenSubagentTurns := make(map[string]struct{})
	seenSubagentSpawns := make(map[string]struct{})
	seenTurnResponses := make(map[string]struct{})
	seenTurnReasoning := make(map[string]struct{})
	seenTurnSkills := make(map[string]struct{})
	seenPlans := 0
	var highest uint64
	for _, entry := range t.entries {
		if entry.ID == "" {
			return errors.New("validate transcript: entry ID is empty")
		}
		if _, exists := seen[entry.ID]; exists {
			return fmt.Errorf("validate transcript: duplicate entry ID %q", entry.ID)
		}
		if entry.Revision == 0 || entry.Revision > t.revision {
			return fmt.Errorf("validate transcript: entry %q has invalid revision %d for thread revision %d", entry.ID, entry.Revision, t.revision)
		}
		if entry.Revision > highest {
			highest = entry.Revision
		}
		if entry.ParentID != "" {
			parentKind, exists := seen[entry.ParentID]
			if !exists {
				return fmt.Errorf("validate transcript: entry %q has unknown or later parent %q", entry.ID, entry.ParentID)
			}
			if parentKind != EntryKinds.ENTRYSUBAGENT && parentKind != EntryKinds.ENTRYTOOLCALL {
				return fmt.Errorf("validate transcript: entry %q has invalid parent kind %s", entry.ID, parentKind)
			}
		}
		if err := validateEntry(entry); err != nil {
			return fmt.Errorf("validate transcript entry %q: %w", entry.ID, err)
		}
		switch entry.Kind {
		case EntryKinds.ENTRYTOOLCALL:
			if _, exists := seenToolIDs[entry.Payload.ToolID]; exists {
				return fmt.Errorf("validate transcript: duplicate tool ID %q", entry.Payload.ToolID)
			}
			seenToolIDs[entry.Payload.ToolID] = struct{}{}
		case EntryKinds.ENTRYASSISTANTMESSAGE:
			if entry.TurnID != "" {
				if _, exists := seenTurnResponses[entry.TurnID]; exists {
					return fmt.Errorf("validate transcript: duplicate assistant entry for turn %q", entry.TurnID)
				}
				seenTurnResponses[entry.TurnID] = struct{}{}
			}
		case EntryKinds.ENTRYREASONING:
			if entry.TurnID != "" {
				if _, exists := seenTurnReasoning[entry.TurnID]; exists {
					return fmt.Errorf("validate transcript: duplicate reasoning entry for turn %q", entry.TurnID)
				}
				seenTurnReasoning[entry.TurnID] = struct{}{}
			}
		case EntryKinds.ENTRYSKILLS:
			if entry.TurnID != "" {
				if _, exists := seenTurnSkills[entry.TurnID]; exists {
					return fmt.Errorf("validate transcript: duplicate skills entry for turn %q", entry.TurnID)
				}
				seenTurnSkills[entry.TurnID] = struct{}{}
			}
		case EntryKinds.ENTRYSUBAGENT:
			if entry.TurnID != "" {
				if _, exists := seenSubagentTurns[entry.TurnID]; exists {
					return fmt.Errorf("validate transcript: duplicate subagent turn ID %q", entry.TurnID)
				}
				seenSubagentTurns[entry.TurnID] = struct{}{}
			}
			if entry.Payload.SpawnToolID != "" {
				if _, exists := seenSubagentSpawns[entry.Payload.SpawnToolID]; exists {
					return fmt.Errorf("validate transcript: duplicate subagent spawn tool ID %q", entry.Payload.SpawnToolID)
				}
				seenSubagentSpawns[entry.Payload.SpawnToolID] = struct{}{}
			}
		case EntryKinds.ENTRYPLAN:
			seenPlans++
			if seenPlans > 1 {
				return errors.New("validate transcript: multiple plan entries")
			}
		}
		seen[entry.ID] = entry.Kind
	}
	if highest != t.revision {
		return fmt.Errorf("validate transcript: highest entry revision %d does not match thread revision %d", highest, t.revision)
	}
	return nil
}

func validateEntry(entry Entry) error {
	p := entry.Payload
	switch entry.Kind {
	case EntryKinds.ENTRYUSERMESSAGE:
		if p.Text == "" && len(p.Attachments) == 0 {
			return errors.New("text and attachments are empty")
		}
		for _, attachment := range p.Attachments {
			if attachment.Name == "" {
				return errors.New("attachment name is empty")
			}
			if attachment.Size < 0 {
				return errors.New("attachment size is negative")
			}
		}
	case EntryKinds.ENTRYQUEUEDUSER, EntryKinds.ENTRYREASONING, EntryKinds.ENTRYNOTICE:
		if p.Text == "" {
			return errors.New("text is empty")
		}
	case EntryKinds.ENTRYASSISTANTMESSAGE:
		if !p.Complete && !p.Interrupted && p.Text == "" {
			return errors.New("assistant entry is empty and nonterminal")
		}
	case EntryKinds.ENTRYTOOLCALL:
		if p.ToolID == "" || p.ToolName == "" {
			return errors.New("tool identity is incomplete")
		}
		if p.ToolState != ToolRunning && p.ToolState != ToolSucceeded && p.ToolState != ToolFailed && p.ToolState != ToolInterrupted {
			return fmt.Errorf("invalid tool state %q", p.ToolState)
		}
		if (p.ToolState == ToolRunning || p.ToolState == ToolInterrupted) && p.DurationMS != 0 {
			return fmt.Errorf("%s tool has duration %d", p.ToolState, p.DurationMS)
		}
		if p.ToolState != ToolFailed && p.FailureKind != "" {
			return fmt.Errorf("%s tool has failure kind %q", p.ToolState, p.FailureKind)
		}
	case EntryKinds.ENTRYDIFF:
		if p.Path == "" {
			return errors.New("diff path is empty")
		}
	case EntryKinds.ENTRYPLAN:
		if len(p.Plan.Steps) == 0 {
			return errors.New("plan has no steps")
		}
	case EntryKinds.ENTRYSKILLS:
		if len(p.Skills) == 0 {
			return errors.New("skills are empty")
		}
		for _, skill := range p.Skills {
			if skill == "" {
				return errors.New("skill name is empty")
			}
		}
	case EntryKinds.ENTRYSUBAGENT:
		if p.AgentName == "" {
			return errors.New("subagent name is empty")
		}
		if p.Subagent != SubagentPending && p.Subagent != SubagentRunning && p.Subagent != SubagentCompleted && p.Subagent != SubagentFailed && p.Subagent != SubagentInterrupted {
			return fmt.Errorf("invalid subagent state %q", p.Subagent)
		}
		if p.Subagent == SubagentPending && entry.TurnID != "" {
			return errors.New("pending subagent has turn ID")
		}
		if p.Subagent != SubagentPending && entry.TurnID == "" {
			return fmt.Errorf("%s subagent turn ID is empty", p.Subagent)
		}
	default:
		return fmt.Errorf("unsupported kind %s", entry.Kind)
	}
	return nil
}
