package taskrunner

import (
	"context"
	"fmt"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// MemoryStore persists facts about people.
type MemoryStore interface {
	StoreMemory(ctx context.Context, personName, fact string) error
}

// StoreMemoryTool lets background tasks store memories.
type StoreMemoryTool struct {
	store MemoryStore
	bus   *events.Bus
}

// StoreMemoryOption customises a StoreMemoryTool at construction.
type StoreMemoryOption func(*StoreMemoryTool)

// WithStoreMemoryBus wires an event bus so stored memories emit events.
func WithStoreMemoryBus(bus *events.Bus) StoreMemoryOption {
	return func(t *StoreMemoryTool) { t.bus = bus }
}

func NewStoreMemoryTool(store MemoryStore, opts ...StoreMemoryOption) *StoreMemoryTool {
	t := &StoreMemoryTool{store: store}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *StoreMemoryTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "store_memory",
		Description: "Store a fact about a person in long-term memory.",
		Parameters: service.Parameters{
			{Name: "person_name", Type: service.ParamString, Description: "Name of the person", Required: true},
			{Name: "fact", Type: service.ParamString, Description: "The fact to remember", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *StoreMemoryTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	person := call.Arguments.String("person_name", "")
	fact := call.Arguments.String("fact", "")
	if person == "" || fact == "" {
		return tools.Failure(call.ID, tools.Validation("store_memory", "person_name and fact are required")), nil
	}
	if t.store == nil {
		return tools.Failure(call.ID, tools.Fatal("store_memory", fmt.Errorf("memory store not configured"))), nil
	}
	if err := t.store.StoreMemory(ctx, person, fact); err != nil {
		return tools.Failure(call.ID, tools.Transient("store_memory", fmt.Errorf("store memory: %w", err))), nil
	}
	if t.bus != nil {
		t.bus.Emit(events.Event{
			Type:    events.MemoryStored,
			Payload: events.TaskFindingPayload{PersonName: person, Finding: fact},
		})
	}
	return tools.Success(call.ID, fmt.Sprintf("Stored memory for %s: %s", person, fact)), nil
}

// TaskSpawner creates new tasks.
type TaskSpawner interface {
	CreateTask(ctx context.Context, prompt, personName, sessionID, schedule, profileName, workspaceName string, maxIterations int) (string, error)
}

// TaskEnqueuer enqueues tasks for execution.
type TaskEnqueuer interface {
	Enqueue(id repository.TaskID)
}

// SpawnTaskTool lets background tasks create follow-up tasks (depth=1 max).
type SpawnTaskTool struct {
	spawner  TaskSpawner
	enqueuer TaskEnqueuer
}

func NewSpawnTaskTool(spawner TaskSpawner, enqueuer TaskEnqueuer) *SpawnTaskTool {
	return &SpawnTaskTool{spawner: spawner, enqueuer: enqueuer}
}

// SetEnqueuer back-fills the enqueuer reference. Callers use this to break
// the action-tools ↔ Runner construction cycle.
func (t *SpawnTaskTool) SetEnqueuer(e TaskEnqueuer) {
	t.enqueuer = e
}

func (t *SpawnTaskTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "spawn_task",
		Description: "Create a follow-up task. Spawned tasks cannot spawn further tasks.",
		Parameters: service.Parameters{
			{Name: "prompt", Type: service.ParamString, Description: "What the task should research or do", Required: true},
			{Name: "schedule", Type: service.ParamString, Description: "Cron expression for recurring tasks (optional)", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *SpawnTaskTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	prompt := call.Arguments.String("prompt", "")
	schedule := call.Arguments.String("schedule", "")
	if prompt == "" {
		return tools.Failure(call.ID, tools.Validation("spawn_task", "prompt is required")), nil
	}
	if t.spawner == nil {
		return tools.Failure(call.ID, tools.Fatal("spawn_task", fmt.Errorf("task spawner not configured"))), nil
	}
	personName := service.PersonNameFromCtx(ctx)
	sessionID := service.SessionIDFromCtx(ctx)
	id, err := t.spawner.CreateTask(ctx, prompt, personName, sessionID, schedule, "", "", 20)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("spawn_task", fmt.Errorf("spawn task: %w", err))), nil
	}
	if schedule == "" && t.enqueuer != nil {
		t.enqueuer.Enqueue(repository.TaskID(id))
	}
	return tools.Success(call.ID, fmt.Sprintf("Spawned task %s", id)), nil
}

// ScheduleUpdater updates task schedules.
type ScheduleUpdater interface {
	UpdateSchedule(ctx context.Context, taskID, schedule string) error
}

// AdjustScheduleTool lets a task modify its own cron schedule.
type AdjustScheduleTool struct {
	updater ScheduleUpdater
}

func NewAdjustScheduleTool(updater ScheduleUpdater) *AdjustScheduleTool {
	return &AdjustScheduleTool{updater: updater}
}

func (t *AdjustScheduleTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "adjust_schedule",
		Description: "Change this task's cron schedule. Use standard cron expressions.",
		Parameters: service.Parameters{
			{Name: "schedule", Type: service.ParamString, Description: "New cron expression (e.g. '0 */2 * * *' for every 2 hours)", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *AdjustScheduleTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	schedule := call.Arguments.String("schedule", "")
	if schedule == "" {
		return tools.Failure(call.ID, tools.Validation("adjust_schedule", "schedule is required")), nil
	}
	taskID := service.SessionIDFromCtx(ctx)
	if taskID == "" {
		return tools.Failure(call.ID, tools.Validation("adjust_schedule", "no task context")), nil
	}
	if t.updater == nil {
		return tools.Failure(call.ID, tools.Fatal("adjust_schedule", fmt.Errorf("schedule updater not configured"))), nil
	}
	if err := t.updater.UpdateSchedule(ctx, taskID, schedule); err != nil {
		return tools.Failure(call.ID, tools.Transient("adjust_schedule", fmt.Errorf("adjust schedule: %w", err))), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("Schedule updated to: %s", schedule)), nil
}

// NotifyUserTool lets background tasks push notifications to the frontend.
type NotifyUserTool struct {
	notifications *znotify.NotificationStore
}

func NewNotifyUserTool(notifications *znotify.NotificationStore) *NotifyUserTool {
	return &NotifyUserTool{notifications: notifications}
}

func (t *NotifyUserTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "notify_user",
		Description: "Send a notification to the user via the frontend.",
		Parameters: service.Parameters{
			{Name: "message", Type: service.ParamString, Description: "The notification message", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *NotifyUserTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	message := call.Arguments.String("message", "")
	if message == "" {
		return tools.Failure(call.ID, tools.Validation("notify_user", "message is required")), nil
	}
	sessionID := service.SessionIDFromCtx(ctx)
	if t.notifications != nil {
		t.notifications.Push(znotify.Notification{
			SessionID: sessionID,
			ToolName:  "notify_user",
			Content:   message,
		})
	}
	return tools.Success(call.ID, "Notification sent."), nil
}

// ProposalStore persists tool proposals.
type ProposalStore interface {
	CreateProposal(ctx context.Context, toolName, description, mcpURL, rationale string) error
}

// MCPEndpointValidator probes an MCP endpoint to confirm it speaks the protocol
// before a proposal is persisted. Return nil if the endpoint is reachable and
// responds to tools/list.
type MCPEndpointValidator func(ctx context.Context, url string) error

// ProposeToolTool lets tasks suggest new MCP tools for human approval.
type ProposeToolTool struct {
	store     ProposalStore
	validator MCPEndpointValidator
	bus       *events.Bus
}

// ProposeToolOption customizes a ProposeToolTool at construction time.
type ProposeToolOption func(*ProposeToolTool)

// WithMCPEndpointValidator sets a pre-flight validator that probes the URL
// before the proposal is persisted. Failed validation aborts the tool call.
func WithMCPEndpointValidator(v MCPEndpointValidator) ProposeToolOption {
	return func(t *ProposeToolTool) { t.validator = v }
}

// WithProposeToolBus wires an event bus for ToolProposed emissions.
func WithProposeToolBus(bus *events.Bus) ProposeToolOption {
	return func(t *ProposeToolTool) { t.bus = bus }
}

func NewProposeToolTool(store ProposalStore, opts ...ProposeToolOption) *ProposeToolTool {
	t := &ProposeToolTool{store: store}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *ProposeToolTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "propose_tool",
		Description: "Propose an MCP server for human approval. When the user mentions an MCP server by name or suggests you need one, call this tool with your best-guess URL — the endpoint is probed before saving, so if the URL is wrong you'll get the error back and can try again. Do not ask the user for a URL unless you genuinely have no idea; try the obvious candidates first (e.g. the server's documented HTTP endpoint, or https://mcp.<vendor>.com/mcp).",
		Parameters: service.Parameters{
			{Name: "tool_name", Type: service.ParamString, Description: "Short identifier for the provider (e.g. 'github', 'deepwiki', 'slack')", Required: true},
			{Name: "description", Type: service.ParamString, Description: "What the tool does", Required: true},
			{Name: "mcp_url", Type: service.ParamString, Description: "Full HTTP(S) URL of the MCP server's streamable-HTTP endpoint (e.g. https://mcp.deepwiki.com/mcp). Guessing is fine — validation runs before save.", Required: true},
			{Name: "rationale", Type: service.ParamString, Description: "Why this tool is needed", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *ProposeToolTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	name := call.Arguments.String("tool_name", "")
	desc := call.Arguments.String("description", "")
	url := call.Arguments.String("mcp_url", "")
	rationale := call.Arguments.String("rationale", "")
	var missing []string
	if name == "" {
		missing = append(missing, "tool_name")
	}
	if desc == "" {
		missing = append(missing, "description")
	}
	if url == "" {
		missing = append(missing, "mcp_url")
	}
	if rationale == "" {
		missing = append(missing, "rationale")
	}
	if len(missing) > 0 {
		return tools.Failure(call.ID, tools.Validation("propose_tool", fmt.Sprintf("missing required fields: %v — retry with all four filled in", missing))), nil
	}
	if t.validator != nil {
		if err := t.validator(ctx, url); err != nil {
			return tools.Failure(call.ID, tools.Validation("propose_tool", fmt.Sprintf("mcp_url %q did not respond to MCP tools/list — not saving. Underlying error: %v. If the server only runs as a stdio subprocess, say so in rationale instead of passing a URL", url, err))), nil
		}
	}
	if t.store != nil {
		if err := t.store.CreateProposal(ctx, name, desc, url, rationale); err != nil {
			return tools.Failure(call.ID, tools.Transient("propose_tool", fmt.Errorf("create proposal: %w", err))), nil
		}
	}
	if t.bus != nil {
		t.bus.Emit(events.Event{
			Type: events.ToolProposed,
			Payload: events.ToolProposedPayload{
				ToolName:    name,
				Description: desc,
				MCPURL:      url,
				Rationale:   rationale,
			},
		})
	}
	return tools.Success(call.ID, fmt.Sprintf("Proposed tool %q — awaiting human approval.", name)), nil
}

// ActivePromptReader reads the current system prompt so a proposal can reference it.
type ActivePromptReader interface {
	GetActivePrompt(ctx context.Context) (id string, content string, err error)
}

// SkillProposalStore persists LLM-authored skill proposals. Defined
// here (consumer-side) so the taskrunner doesn't pull the repository
// package — the real implementation lives in repository.SkillProposalRepo.
type SkillProposalStore interface {
	CreateSkillProposal(ctx context.Context, proposal SkillProposalInput) error
}

// SkillProposalInput is the flat shape propose_skill writes. Plain
// strings on the wire — the store maps it to whatever persistence
// needs (UUID generation, nullable column handling).
type SkillProposalInput struct {
	TargetSkillID string // empty for new skill
	Name          string
	Description   string
	Markdown      string
	Binding       string // empty = global
	Rationale     string
}

// ProposeSkillTool lets the agent suggest a new skill (capability
// guide) or an update to an existing one. The proposal is inert until
// a human approves it in the admin UI — mirrors the
// request_prompt_update flow because the same "suggest-then-approve"
// pattern fits self-modification of the agent's behaviour.
type ProposeSkillTool struct {
	store SkillProposalStore
}

func NewProposeSkillTool(store SkillProposalStore) *ProposeSkillTool {
	return &ProposeSkillTool{store: store}
}

func (t *ProposeSkillTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: "propose_skill",
		Description: "Propose a new procedural capability (\"skill\") for yourself, or an update to an existing one. A skill is a short markdown document describing HOW you should handle a recurring kind of request — a reusable procedure, not personal information. It gets injected into your system prompt when the user's message semantically matches.\n\n" +
			"USE propose_skill for PROCEDURES (how-to knowledge): \"how to structure a research report\", \"how to triage a home automation glitch\", \"how to present a shopping comparison\".\n\n" +
			"DO NOT use propose_skill for FACTS about the user — those go to the `remember` tool instead: a user's preferences, their schedule, their pets, their currency preference, their dietary restrictions. If a piece of information is about WHO the user is or WHAT they like, it's a memory, not a skill. A well-written skill can REFERENCE memories (e.g. \"check their stored preferences before picking sources\") but never bakes them in.\n\n" +
			"The proposal is saved for human approval and does not take effect until approved.",
		Parameters: service.Parameters{
			{Name: "name", Type: service.ParamString, Description: "Short snake_case identifier for the procedure (e.g. research_report_format, debug_home_automation).", Required: true},
			{Name: "description", Type: service.ParamString, Description: "One sentence describing when this skill should fire. Phrase it in terms of the KIND OF TASK (\"when producing a comparison report\"), not in terms of a user preference.", Required: true},
			{Name: "markdown", Type: service.ParamString, Description: "The procedure in markdown: ordered steps or a checklist. Describe HOW to do the thing; if user preferences are involved, refer to memories rather than embedding specific values.", Required: true},
			{Name: "binding", Type: service.ParamString, Description: "Profile the skill applies to: 'default' for live chat, 'researcher' for research tasks, 'coder' for coding tasks, or omit for global (applies to all).", Required: false, Enum: []string{"default", "researcher", "coder"}},
			{Name: "rationale", Type: service.ParamString, Description: "Why this procedure is worth codifying — what pattern you noticed that would benefit from a reusable recipe.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *ProposeSkillTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	name := call.Arguments.String("name", "")
	description := call.Arguments.String("description", "")
	markdown := call.Arguments.String("markdown", "")
	binding := call.Arguments.String("binding", "")
	rationale := call.Arguments.String("rationale", "")
	if name == "" || description == "" || markdown == "" || rationale == "" {
		return tools.Failure(call.ID, tools.Validation("propose_skill", "name, description, markdown, and rationale are required")), nil
	}
	if err := t.store.CreateSkillProposal(ctx, SkillProposalInput{
		Name:        name,
		Description: description,
		Markdown:    markdown,
		Binding:     binding,
		Rationale:   rationale,
	}); err != nil {
		return tools.Failure(call.ID, tools.Transient("propose_skill", fmt.Errorf("create skill proposal: %w", err))), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("Proposed skill %q — awaiting human approval.", name)), nil
}

// ReadSystemPromptTool exposes the current active system prompt to the
// agent. Without this, request_prompt_update asks the model to write a
// replacement prompt blind — which produces proposals that drop
// existing constraints the model never knew about. Pairing read + write
// lets the agent quote, refactor, or minimally diff the prompt
// intentionally.
type ReadSystemPromptTool struct {
	prompts ActivePromptReader
}

func NewReadSystemPromptTool(prompts ActivePromptReader) *ReadSystemPromptTool {
	return &ReadSystemPromptTool{prompts: prompts}
}

func (t *ReadSystemPromptTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "read_system_prompt",
		Description: "Read the current active system prompt verbatim. Call this before proposing a prompt update so the rewrite is informed by what's actually there.",
		Parameters:  service.Parameters{}.ToJSONSchema(),
	}
}

func (t *ReadSystemPromptTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	id, content, err := t.prompts.GetActivePrompt(ctx)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("read_system_prompt", fmt.Errorf("read active prompt: %w", err))), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("active prompt id: %s\n---\n%s", id, content)), nil
}

// PromptProposalStore persists prompt update proposals.
type PromptProposalStore interface {
	CreatePromptProposal(ctx context.Context, currentPromptID, proposedContent, rationale string) error
}

// RequestPromptUpdateTool lets the agent suggest a rewrite of the active system
// prompt. The proposal is inert until a human approves it from the admin panel.
type RequestPromptUpdateTool struct {
	prompts ActivePromptReader
	store   PromptProposalStore
	bus     *events.Bus
}

// RequestPromptUpdateOption customises a RequestPromptUpdateTool at construction.
type RequestPromptUpdateOption func(*RequestPromptUpdateTool)

// WithRequestPromptUpdateBus wires an event bus so accepted prompt proposals emit events.
func WithRequestPromptUpdateBus(bus *events.Bus) RequestPromptUpdateOption {
	return func(t *RequestPromptUpdateTool) { t.bus = bus }
}

func NewRequestPromptUpdateTool(prompts ActivePromptReader, store PromptProposalStore, opts ...RequestPromptUpdateOption) *RequestPromptUpdateTool {
	t := &RequestPromptUpdateTool{prompts: prompts, store: store}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *RequestPromptUpdateTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "request_prompt_update",
		Description: "Propose a rewrite of your own system prompt. Call read_system_prompt first so your proposal preserves the existing constraints you want to keep. The proposal is saved for human approval and does not take effect until approved.",
		Parameters: service.Parameters{
			{Name: "proposed_content", Type: service.ParamString, Description: "The full replacement system prompt", Required: true},
			{Name: "rationale", Type: service.ParamString, Description: "Why this change is needed", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *RequestPromptUpdateTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	proposed := call.Arguments.String("proposed_content", "")
	rationale := call.Arguments.String("rationale", "")
	if proposed == "" || rationale == "" {
		return tools.Failure(call.ID, tools.Validation("request_prompt_update", "proposed_content and rationale are required")), nil
	}
	id, _, err := t.prompts.GetActivePrompt(ctx)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("request_prompt_update", fmt.Errorf("resolve active prompt: %w", err))), nil
	}
	if err := t.store.CreatePromptProposal(ctx, id, proposed, rationale); err != nil {
		return tools.Failure(call.ID, tools.Transient("request_prompt_update", fmt.Errorf("create prompt proposal: %w", err))), nil
	}
	if t.bus != nil {
		t.bus.Emit(events.Event{
			Type:    events.PromptProposed,
			Payload: events.PromptProposedPayload{CurrentPromptID: id, Rationale: rationale},
		})
	}
	return tools.Success(call.ID, "Prompt update proposed — awaiting human approval."), nil
}
