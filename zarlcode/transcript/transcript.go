// Package transcript defines the durable, renderer-independent human thread.
package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/zsync"
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
	// SubagentInterrupted means process termination ended a pending spawn or running child.
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
	payload.TextBreaks = append([]int(nil), payload.TextBreaks...)
	payload.Skills = append([]string(nil), payload.Skills...)
	payload.Attachments = append([]Attachment(nil), payload.Attachments...)
	payload.Control = payload.Control.Clone()
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

// InputAdmission identifies a result admitted to a parent's model context. It
// records admission, not durable runner-history delivery or live task ownership.
type InputAdmission struct {
	Namespace string `json:"namespace"`
	ID        string `json:"id"`
	Explicit  bool   `json:"explicit,omitempty"`
}

// Payload carries kind-specific durable facts. Fields unrelated to Kind remain zero.
type Payload struct {
	Text        string `json:"text,omitempty"`
	TextBreaks  []int  `json:"text_breaks,omitempty"`
	Injected    bool   `json:"injected,omitempty"`
	Complete    bool   `json:"complete,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`

	ToolID       string `json:"tool_id,omitempty"`
	ParentToolID string `json:"parent_tool_id,omitempty"`
	// Execution links correlate inspector data; Entry.ID/ParentID own hierarchy.
	ExecutionID       string    `json:"execution_id,omitempty"`
	ParentExecutionID string    `json:"parent_execution_id,omitempty"`
	ToolName          string    `json:"tool_name,omitempty"`
	Argument          string    `json:"argument,omitempty"`
	ToolInput         string    `json:"tool_input,omitempty"`
	ToolOutputPreview string    `json:"tool_output_preview,omitempty"`
	Effect            string    `json:"effect,omitempty"`
	ToolState         ToolState `json:"tool_state,omitempty"`
	FailureKind       string    `json:"failure_kind,omitempty"`
	DurationMS        int64     `json:"duration_ms,omitempty"`
	Sequence          int       `json:"sequence,omitempty"`

	Path        string       `json:"path,omitempty"`
	Diff        string       `json:"diff,omitempty"`
	Plan        code.Plan    `json:"plan,omitzero"`
	Skills      []string     `json:"skills,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`

	AgentName        string        `json:"agent_name,omitempty"`
	Provider         string        `json:"provider,omitempty"`
	Model            string        `json:"model,omitempty"`
	Prompt           string        `json:"prompt,omitempty"`
	SpawnToolID      string        `json:"spawn_tool_id,omitempty"`
	SpawnExecutionID string        `json:"spawn_execution_id,omitempty"`
	Subagent         SubagentState `json:"subagent,omitempty"`

	InputAdmission InputAdmission       `json:"input_admission,omitzero"`
	InputWaiting   bool                 `json:"input_waiting,omitempty"`
	Control        runner.ReplayControl `json:"control,omitzero"`
}

// TextSegments returns independent Markdown source segments without changing Text.
// An absent boundary list returns Text as one segment; a boundary at the end does
// not add an empty trailing segment.
func (p Payload) TextSegments() []string {
	segments := make([]string, 0, len(p.TextBreaks)+1)
	start := 0
	for _, end := range p.TextBreaks {
		segments = append(segments, p.Text[start:end])
		start = end
	}
	if start < len(p.Text) || len(segments) == 0 {
		segments = append(segments, p.Text[start:])
	}
	return segments
}

// Builder mutates one Thread while maintaining stable semantic identities.
type Builder struct {
	thread         Thread
	nextID         int
	turnResponse   map[string]string
	turnReason     map[string]string
	turnSkills     map[string]string
	tools          map[string]string
	legacyTools    map[string][]string
	subagents      map[string]string
	spawnAgents    map[string]string
	legacySpawns   map[string][]string
	currentPlan    string
	positions      map[string]int
	collectChanges bool
	changed        []int
}

// NewBuilder creates an empty canonical thread builder.
func NewBuilder() *Builder {
	return &Builder{
		positions:    make(map[string]int),
		turnResponse: make(map[string]string),
		turnReason:   make(map[string]string),
		turnSkills:   make(map[string]string),
		tools:        make(map[string]string),
		legacyTools:  make(map[string][]string),
		subagents:    make(map[string]string),
		spawnAgents:  make(map[string]string),
		legacySpawns: make(map[string][]string),
	}
}

// NewBuilderFrom creates a builder over an existing validated thread.
func NewBuilderFrom(thread Thread) *Builder {
	builder := NewBuilder()
	builder.thread = Thread{revision: thread.Revision(), entries: thread.Entries()}
	activeTurnSegments := zsync.NewSet[string]()
	for index, entry := range builder.thread.entries {
		builder.positions[entry.ID] = index
		if number, ok := generatedEntryNumber(entry.ID); ok && number > builder.nextID {
			builder.nextID = number
		}
		switch entry.Kind {
		case EntryKinds.ENTRYASSISTANTMESSAGE:
			if entry.TurnID == "" {
				break
			}
			delete(builder.turnSkills, entry.TurnID)
			activeTurnSegments.Remove(entry.TurnID)
			if !entry.Payload.Complete && !entry.Payload.Interrupted {
				builder.turnResponse[entry.TurnID] = entry.ID
				activeTurnSegments.Add(entry.TurnID)
			}
		case EntryKinds.ENTRYREASONING:
			if !entry.Payload.Complete && !entry.Payload.Interrupted && entry.TurnID != "" {
				builder.turnReason[entry.TurnID] = entry.ID
			}
		case EntryKinds.ENTRYSKILLS:
			if entry.TurnID != "" && activeTurnSegments.Contains(entry.TurnID) {
				builder.turnSkills[entry.TurnID] = entry.ID
			}
		case EntryKinds.ENTRYTOOLCALL:
			if entry.Payload.ToolState == ToolRunning {
				if entry.Payload.ExecutionID != "" {
					builder.tools[entry.Payload.ExecutionID] = entry.ID
				} else {
					builder.legacyTools[entry.Payload.ToolID] = append(builder.legacyTools[entry.Payload.ToolID], entry.ID)
				}
			}
		case EntryKinds.ENTRYSUBAGENT:
			if entry.TurnID != "" && entry.Payload.Subagent == SubagentRunning {
				builder.subagents[entry.TurnID] = entry.ID
			}
			if entry.Payload.SpawnToolID != "" && entry.Payload.Subagent == SubagentPending {
				if entry.Payload.SpawnExecutionID != "" {
					builder.spawnAgents[entry.Payload.SpawnExecutionID] = entry.ID
				} else {
					builder.legacySpawns[entry.Payload.SpawnToolID] = append(builder.legacySpawns[entry.Payload.SpawnToolID], entry.ID)
				}
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
	id := fmt.Sprintf("e%d", b.nextID)
	b.thread.entries = append(b.thread.entries, Entry{ID: id, ParentID: parentID, TurnID: turnID, Kind: kind, Payload: payload})
	index := len(b.thread.entries) - 1
	b.positions[id] = index
	b.touch(index)
	return id
}

func (b *Builder) recordControl(turnID string, control runner.ReplayControl) (string, error) {
	encoded, err := encodeControl(control)
	if err != nil {
		return "", err
	}
	for _, entry := range b.thread.entries {
		if entry.ID != control.ID {
			continue
		}
		if entry.Kind != EntryKinds.ENTRYCONTROLCHANGE {
			return "", errors.New("control identity collides with another entry")
		}
		existing, err := encodeControl(entry.Payload.Control)
		if err != nil || !bytes.Equal(existing, encoded) {
			return "", errors.New("control identity has different payload")
		}
		return entry.ID, nil
	}
	if number, ok := generatedEntryNumber(control.ID); ok && number > b.nextID {
		b.nextID = number
	}
	b.thread.entries = append(b.thread.entries, Entry{
		ID: control.ID, TurnID: turnID, Kind: EntryKinds.ENTRYCONTROLCHANGE,
		Payload: Payload{Control: control.Clone()},
	})
	index := len(b.thread.entries) - 1
	b.positions[control.ID] = index
	b.touch(index)
	return control.ID, nil
}

func encodeControl(control runner.ReplayControl) ([]byte, error) {
	return runner.MarshalReplayMessage(runner.ReplayMessage{
		Kind: runner.ReplayOccurrenceKinds.CONTROL, Control: control,
	})
}

func (b *Builder) update(id string, update func(*Entry)) {
	if index, ok := b.positions[id]; ok {
		update(&b.thread.entries[index])
		b.touch(index)
	}
}

// touch is the only runtime revision-stamping boundary. Reducers collect entry
// positions for one event; standalone builders do not retain a mutation log.
func (b *Builder) touch(index int) {
	b.thread.revision++
	b.thread.entries[index].Revision = b.thread.revision
	if b.collectChanges {
		b.changed = append(b.changed, index)
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

// FinishAssistantSegment records the end of a non-empty assistant Markdown source
// segment without closing the turn or any other turn-owned stream.
func (b *Builder) FinishAssistantSegment(turnID string) {
	index, ok := b.positions[b.turnResponse[turnID]]
	if !ok {
		return
	}
	entry := b.thread.entries[index]
	// JSON replaces invalid UTF-8; do not persist unsafe source offsets.
	if !utf8.ValidString(entry.Payload.Text) {
		return
	}
	end := len(entry.Payload.Text)
	if end == 0 || len(entry.Payload.TextBreaks) > 0 && entry.Payload.TextBreaks[len(entry.Payload.TextBreaks)-1] == end {
		return
	}
	entry.Payload.TextBreaks = append(entry.Payload.TextBreaks, end)
	b.thread.entries[index] = entry
	b.touch(index)
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
	b.finishTurn(turnID, false)
}

func (b *Builder) finishTurn(turnID string, interrupted bool) {
	for _, id := range []string{b.turnResponse[turnID], b.turnReason[turnID]} {
		if id != "" {
			b.update(id, func(entry *Entry) {
				entry.Payload.Complete = !interrupted
				entry.Payload.Interrupted = interrupted
			})
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

// StartTool records one legacy tool call keyed by its provider ID.
func (b *Builder) StartTool(turnID, parentID, toolID, parentToolID, name, argument string, sequence int) string {
	return b.StartToolExecution(turnID, parentID, "", toolID, "", parentToolID, name, argument, sequence)
}

// StartToolExecution records one tool invocation under its exact runner identity.
func (b *Builder) StartToolExecution(turnID, parentID, executionID, toolID, parentExecutionID, parentToolID, name, argument string, sequence int) string {
	return b.startToolExecution(turnID, parentID, executionID, toolID, parentExecutionID, parentToolID, name, argument, "", sequence)
}

func (b *Builder) startToolExecution(turnID, parentID, executionID, toolID, parentExecutionID, parentToolID, name, argument, input string, sequence int) string {
	id := b.append(EntryKinds.ENTRYTOOLCALL, parentID, turnID, Payload{
		ToolID: toolID, ParentToolID: parentToolID, ToolName: name,
		ExecutionID: executionID, ParentExecutionID: parentExecutionID,
		Argument: argument, ToolInput: input, ToolState: ToolRunning, Sequence: sequence,
	})
	if executionID != "" {
		b.tools[executionID] = id
	} else {
		b.legacyTools[toolID] = append(b.legacyTools[toolID], id)
	}
	return id
}

// ToolEntryID returns the most recent legacy entry for a provider tool ID.
func (b *Builder) ToolEntryID(toolID string) string {
	ids := b.legacyTools[toolID]
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
}

// ToolExecutionEntryID returns the entry for one exact invocation.
func (b *Builder) ToolExecutionEntryID(executionID, toolID string) string {
	if executionID != "" {
		return b.tools[executionID]
	}
	return b.ToolEntryID(toolID)
}

// UpdateToolOutputPreview replaces the bounded preview for one active exact invocation.
// Unknown, legacy, mismatched, and terminal invocations are ignored.
func (b *Builder) UpdateToolOutputPreview(executionID, toolID, output string) {
	if executionID == "" {
		return
	}
	index, ok := b.positions[b.tools[executionID]]
	if !ok {
		return
	}
	entry := b.thread.entries[index]
	if entry.Payload.ExecutionID != executionID || entry.Payload.ToolID != toolID ||
		entry.Payload.ToolState != ToolRunning || entry.Payload.ToolOutputPreview == output {
		return
	}
	entry.Payload.ToolOutputPreview = output
	b.thread.entries[index] = entry
	b.touch(index)
}

// FinishTool records one legacy tool call's durable terminal facts.
func (b *Builder) FinishTool(toolID, effect, failureKind string, durationMS int64, failed bool) {
	b.FinishToolExecution("", toolID, effect, failureKind, durationMS, failed)
}

// FinishToolExecution records terminal facts on the exact invocation.
func (b *Builder) FinishToolExecution(executionID, toolID, effect, failureKind string, durationMS int64, failed bool) {
	id := b.tools[executionID]
	if executionID != "" {
		delete(b.tools, executionID)
	} else {
		ids := b.legacyTools[toolID]
		if len(ids) == 0 {
			return
		}
		id = ids[0]
		if len(ids) == 1 {
			delete(b.legacyTools, toolID)
		} else {
			b.legacyTools[toolID] = ids[1:]
		}
	}
	if id == "" {
		return
	}
	b.update(id, func(entry *Entry) {
		entry.Payload.ToolState = ToolSucceeded
		if failed {
			entry.Payload.ToolState = ToolFailed
		}
		entry.Payload.Effect = effect
		entry.Payload.FailureKind = ""
		if failed {
			entry.Payload.FailureKind = failureKind
		}
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

// ReserveSubagent records a legacy requested child keyed by provider tool ID.
func (b *Builder) ReserveSubagent(spawnToolID, agentName, prompt string) string {
	return b.ReserveSubagentExecution("", spawnToolID, agentName, prompt)
}

// ReserveSubagentExecution records a requested child under its exact spawn invocation.
func (b *Builder) ReserveSubagentExecution(spawnExecutionID, spawnToolID, agentName, prompt string) string {
	id := b.append(EntryKinds.ENTRYSUBAGENT, "", "", Payload{
		AgentName: agentName, Prompt: prompt, SpawnToolID: spawnToolID, Subagent: SubagentPending,
		SpawnExecutionID: spawnExecutionID,
	})
	if spawnExecutionID != "" {
		b.spawnAgents[spawnExecutionID] = id
	} else if spawnToolID != "" {
		b.legacySpawns[spawnToolID] = append(b.legacySpawns[spawnToolID], id)
	}
	return id
}

// StartSubagent binds a legacy reserved child task.
func (b *Builder) StartSubagent(turnID, spawnToolID, agentName, provider, model, prompt string) string {
	return b.StartSubagentExecution(turnID, "", spawnToolID, agentName, provider, model, prompt)
}

// StartSubagentExecution binds the child to its exact spawn invocation.
func (b *Builder) StartSubagentExecution(turnID, spawnExecutionID, spawnToolID, agentName, provider, model, prompt string) string {
	id := b.spawnAgents[spawnExecutionID]
	if spawnExecutionID != "" {
		delete(b.spawnAgents, spawnExecutionID)
	} else {
		ids := b.legacySpawns[spawnToolID]
		if len(ids) != 0 {
			id = ids[0]
			if len(ids) == 1 {
				delete(b.legacySpawns, spawnToolID)
			} else {
				b.legacySpawns[spawnToolID] = ids[1:]
			}
		}
	}
	if id == "" {
		id = b.ReserveSubagentExecution(spawnExecutionID, spawnToolID, agentName, prompt)
		if spawnExecutionID != "" {
			delete(b.spawnAgents, spawnExecutionID)
		} else if spawnToolID != "" {
			ids := b.legacySpawns[spawnToolID]
			if len(ids) == 1 {
				delete(b.legacySpawns, spawnToolID)
			} else {
				b.legacySpawns[spawnToolID] = ids[:len(ids)-1]
			}
		}
	}
	b.update(id, func(entry *Entry) {
		entry.TurnID = turnID
		entry.Payload.AgentName = agentName
		entry.Payload.Provider = provider
		entry.Payload.Model = model
		entry.Payload.Prompt = prompt
		entry.Payload.Subagent = SubagentRunning
	})
	b.subagents[turnID] = id
	return id
}

// FinishSubagent marks a child task terminal and settles only its owned streams.
// Failed or interrupted children retain partial output as interrupted, not complete.
func (b *Builder) FinishSubagent(turnID string, status SubagentState) {
	id := b.subagents[turnID]
	if id == "" {
		return // duplicate or unknown terminal event owns no active child
	}
	b.finishTurn(turnID, status != SubagentCompleted)
	b.update(id, func(entry *Entry) { entry.Payload.Subagent = status })
	delete(b.subagents, turnID)
}

// FailSubagent marks a legacy reserved spawn terminal before the child starts.
func (b *Builder) FailSubagent(spawnToolID, detail string) {
	b.FailSubagentExecution("", spawnToolID, detail)
}

// FailSubagentExecution marks the exact reserved spawn terminal before child start.
func (b *Builder) FailSubagentExecution(spawnExecutionID, spawnToolID, detail string) {
	id := b.spawnAgents[spawnExecutionID]
	if spawnExecutionID != "" {
		delete(b.spawnAgents, spawnExecutionID)
	} else {
		ids := b.legacySpawns[spawnToolID]
		if len(ids) == 0 {
			return
		}
		id = ids[0]
		if len(ids) == 1 {
			delete(b.legacySpawns, spawnToolID)
		} else {
			b.legacySpawns[spawnToolID] = ids[1:]
		}
	}
	if id == "" {
		return
	}
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
	seen := zsync.NewMap[string, EntryKind]()
	seenSubagentTurns := zsync.NewSet[string]()
	seenActiveTurnResponses := zsync.NewSet[string]()
	seenActiveTurnReasoning := zsync.NewSet[string]()
	seenPlans := 0
	var highest uint64
	for _, entry := range t.entries {
		if entry.ID == "" {
			return errors.New("validate transcript: entry ID is empty")
		}
		if _, err := seen.Get(entry.ID); err == nil {
			return fmt.Errorf("validate transcript: duplicate entry ID %q", entry.ID)
		}
		if entry.Revision == 0 || entry.Revision > t.revision {
			return fmt.Errorf("validate transcript: entry %q has invalid revision %d for thread revision %d", entry.ID, entry.Revision, t.revision)
		}
		if entry.Revision > highest {
			highest = entry.Revision
		}
		if entry.ParentID != "" {
			parentKind, err := seen.Get(entry.ParentID)
			if err != nil {
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
		case EntryKinds.ENTRYASSISTANTMESSAGE:
			if entry.TurnID != "" && !entry.Payload.Complete && !entry.Payload.Interrupted {
				if !seenActiveTurnResponses.AddIfAbsent(entry.TurnID) {
					return fmt.Errorf("validate transcript: duplicate active assistant entry for turn %q", entry.TurnID)
				}
			}
		case EntryKinds.ENTRYREASONING:
			if entry.TurnID != "" && !entry.Payload.Complete && !entry.Payload.Interrupted {
				if !seenActiveTurnReasoning.AddIfAbsent(entry.TurnID) {
					return fmt.Errorf("validate transcript: duplicate active reasoning entry for turn %q", entry.TurnID)
				}
			}
		case EntryKinds.ENTRYSUBAGENT:
			if entry.TurnID != "" {
				if !seenSubagentTurns.AddIfAbsent(entry.TurnID) {
					return fmt.Errorf("validate transcript: duplicate subagent turn ID %q", entry.TurnID)
				}
			}
		case EntryKinds.ENTRYPLAN:
			seenPlans++
			if seenPlans > 1 {
				return errors.New("validate transcript: multiple plan entries")
			}
		}
		seen.Set(entry.ID, entry.Kind)
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
		if err := validateAttachments(p.Attachments); err != nil {
			return err
		}
	case EntryKinds.ENTRYQUEUEDUSER, EntryKinds.ENTRYREASONING, EntryKinds.ENTRYNOTICE:
		if p.Text == "" {
			return errors.New("text is empty")
		}
	case EntryKinds.ENTRYINPUTADMISSION:
		if p.Text == "" || p.InputAdmission.Namespace == "" || p.InputAdmission.ID == "" {
			return errors.New("input admission identity or text is empty")
		}
	case EntryKinds.ENTRYCONTROLCHANGE:
		return validateControlEntry(entry)
	case EntryKinds.ENTRYINPUTWAIT:
		if p.Text == "" {
			return errors.New("input wait text is empty")
		}
	case EntryKinds.ENTRYASSISTANTMESSAGE:
		if err := validateAssistantText(p); err != nil {
			return err
		}
	case EntryKinds.ENTRYTOOLCALL:
		if err := validateToolPayload(p); err != nil {
			return err
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
			if p.Subagent != SubagentFailed && p.Subagent != SubagentInterrupted {
				return fmt.Errorf("%s subagent turn ID is empty", p.Subagent)
			}
			if p.SpawnToolID == "" {
				return fmt.Errorf("%s unstarted subagent spawn reference is empty", p.Subagent)
			}
		}
	default:
		return fmt.Errorf("unsupported kind %s", entry.Kind)
	}
	if entry.Kind != EntryKinds.ENTRYASSISTANTMESSAGE && len(p.TextBreaks) != 0 {
		return errors.New("text breaks belong only to assistant entries")
	}
	if entry.Kind != EntryKinds.ENTRYTOOLCALL && p.ToolInput != "" {
		return errors.New("tool input belongs only to tool entries")
	}
	if entry.Kind != EntryKinds.ENTRYTOOLCALL && p.ToolOutputPreview != "" {
		return errors.New("tool output preview belongs only to tool entries")
	}
	return nil
}

func validateToolPayload(payload Payload) error {
	if payload.ToolID == "" || payload.ToolName == "" {
		return errors.New("tool identity is incomplete")
	}
	if payload.ToolState != ToolRunning && payload.ToolState != ToolSucceeded && payload.ToolState != ToolFailed && payload.ToolState != ToolInterrupted {
		return fmt.Errorf("invalid tool state %q", payload.ToolState)
	}
	if (payload.ToolState == ToolRunning || payload.ToolState == ToolInterrupted) && payload.DurationMS != 0 {
		return fmt.Errorf("%s tool has duration %d", payload.ToolState, payload.DurationMS)
	}
	if payload.ToolState != ToolFailed && payload.FailureKind != "" {
		return fmt.Errorf("%s tool has failure kind %q", payload.ToolState, payload.FailureKind)
	}
	return validateToolOutputPreview(payload.ToolOutputPreview)
}

func validateToolOutputPreview(output string) error {
	if !utf8.ValidString(output) {
		return errors.New("tool output preview is invalid UTF-8")
	}
	if len(output) > tools.MaxToolOutputPreviewBytes {
		return fmt.Errorf("tool output preview exceeds %d bytes", tools.MaxToolOutputPreviewBytes)
	}
	return nil
}

// validateAttachments is shared by live events and persisted user records so
// their external attachment metadata obeys the same entry-boundary contract.
func validateAttachments(attachments []Attachment) error {
	for _, attachment := range attachments {
		if attachment.Name == "" {
			return errors.New("attachment name is empty")
		}
		if attachment.Size < 0 {
			return errors.New("attachment size is negative")
		}
	}
	return nil
}

func validateAssistantText(payload Payload) error {
	if !payload.Complete && !payload.Interrupted && payload.Text == "" {
		return errors.New("assistant entry is empty and nonterminal")
	}
	return validateTextBreaks(payload.Text, payload.TextBreaks)
}

func validateTextBreaks(text string, breaks []int) error {
	previous := 0
	for _, offset := range breaks {
		if offset <= previous || offset > len(text) {
			return errors.New("text breaks are not strictly increasing within text")
		}
		if offset < len(text) && !utf8.RuneStart(text[offset]) {
			return errors.New("text break splits UTF-8 encoding")
		}
		previous = offset
	}
	return nil
}
